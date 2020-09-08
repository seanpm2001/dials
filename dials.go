package dials

import (
	"context"
	"fmt"
	"io"
	"reflect"
	"sync/atomic"

	"github.com/vimeo/dials/ptrify"
)

// WatchedErrorHandler is a callback that's called when something fails when
// dials is operating in a watching mode.  Both oldConfig and NewConfig are
// guaranteed to be populated with the same pointer-type that was passed to
// `Config()`.
type WatchedErrorHandler func(ctx context.Context, err error, oldConfig, newConfig interface{})

// Params provides options for setting Dials's behavior in some cases.
type Params struct {
	// OnWatchedError is called when either of several conditions are met:
	//  - There is an error re-stacking the configuration
	//  -
	//  - a Verify() method fails after re-stacking when a new version is
	//    provided by a watching source
	OnWatchedError WatchedErrorHandler
}

// Config populates the passed in config struct by reading the values from the
// different Sources. The order of the sources denotes the precedence of the formats
// so the last source passed to the function has the ability to override fields that
// were set by previous sources
//
// If present, a Verify() method will be called after each stacking attempt.
// Blocking/expensive work should not be done in this method. (see the comment
// on Verify()) in VerifiedConfig for details)
//
// If complicated/blocking initialization/verification is necessary, one can either:
//  - If not using any watching sources, do any verification with the returned
//    config from Config.
//  - If using at least one watching source, configure a goroutine to watch the
//    channel returned by the `Dials.Events()` method that does its own
//    installation after verifying the config.
//
// More complicated verification/initialization should be done by
// consuming from the channel returned by `Events()`.
func (p Params) Config(ctx context.Context, t interface{}, sources ...Source) (*Dials, error) {

	watcherChan := make(chan *watchTab)
	computed := make([]sourceValue, 0, len(sources))

	typeOfT := reflect.TypeOf(t)
	if typeOfT.Kind() != reflect.Ptr {
		return nil, fmt.Errorf("config type %T is not a pointer", t)
	}

	tVal := realDeepCopy(t)

	typeInstance := &Type{ptrify.Pointerify(typeOfT.Elem(), tVal.Elem())}
	someoneWatching := false
	for _, source := range sources {
		s := source

		v, err := source.Value(typeInstance)
		if err != nil {
			return nil, err
		}
		computed = append(computed, sourceValue{
			source: s,
			value:  v,
		})

		if w, ok := source.(Watcher); ok {
			someoneWatching = true
			err = w.Watch(ctx, typeInstance, func(ctx context.Context, v reflect.Value) {
				select {
				case <-ctx.Done():
				case watcherChan <- &watchTab{source: s, value: v}:
				}
			})
			if err != nil {
				return nil, err
			}
		}
	}

	newValue, err := compose(tVal.Interface(), computed)
	if err != nil {
		return nil, err
	}

	d := &Dials{
		value:       atomic.Value{},
		updatesChan: make(chan interface{}, 1),
		params:      p,
	}
	d.value.Store(newValue)

	// Verify that the configuration is valid if a Verify() method is present.
	if vf, ok := newValue.(VerifiedConfig); ok {
		if vfErr := vf.Verify(); vfErr != nil {
			return nil, fmt.Errorf("Initial configuration verification failed: %w", vfErr)
		}
	}

	if someoneWatching {
		go d.monitor(ctx, tVal.Interface(), computed, watcherChan)
	}
	return d, nil
}

// Config populates the passed in config struct by reading the values from the
// different Sources. The order of the sources denotes the precedence of the formats
// so the last source passed to the function has the ability to override fields that
// were set by previous sources
// This top-level function is present for convenience and backwards
// compatibility when there is no need to specify an error-handler.
func Config(ctx context.Context, t interface{}, sources ...Source) (*Dials, error) {
	return Params{}.Config(ctx, t, sources...)
}

// Source interface is implemented by each configuration source that is used to
// populate the config struct such as environment variables, command line flags,
// config files, and more
type Source interface {
	Value(*Type) (reflect.Value, error)
}

// Decoder interface is implemented by different data formats to read the config
// files, decode the data, and insert the values in the config struct. Dials
// currently supports YAML, JSON, and TOML data formats.
type Decoder interface {
	Decode(io.Reader, *Type) (reflect.Value, error)
}

type watchTab struct {
	source Source
	value  reflect.Value
}

// Watcher should be implemented by Sources that allow their configuration to be
// watched for changes.
type Watcher interface {
	Watch(context.Context, *Type, func(context.Context, reflect.Value)) error
}

// VerifiedConfig implements the Verify method, allowing Dials to execute the
// Verify method before returning/installing a new version of the
// configuration.
type VerifiedConfig interface {
	// Verify() should return a non-nil error if the configuration is
	// invalid.
	// As this method is called any time the configuration sources are
	// restacked, it should not do any complex or blocking work.
	Verify() error
}

// Dials is the main access point for your configuration.
type Dials struct {
	value       atomic.Value
	updatesChan chan interface{}
	params      Params
}

// View returns the configuration struct populated.
func (d *Dials) View() interface{} {
	return d.value.Load()
}

// Events returns a channel that will get a message every time the configuration
// is updated.
func (d *Dials) Events() <-chan interface{} {
	return d.updatesChan
}

// Fill populates the passed struct with the current value of the configuration.
// It will panic if the type of `blankConfig` does not match the type of the
// configuration value passed to `Config` in the first place.
func (d *Dials) Fill(blankConfig interface{}) {
	bVal := reflect.ValueOf(blankConfig)
	currentVal := reflect.ValueOf(d.value.Load())

	if bVal.Type() != currentVal.Type() {
		panic(fmt.Sprintf(
			"value to fill type (%s) does not match actual type (%s)",
			bVal.Type(),
			currentVal.Type(),
		))
	}

	bVal.Elem().Set(currentVal.Elem())
}

func (d *Dials) monitor(
	ctx context.Context,
	t interface{},
	sourceValues []sourceValue,
	watcherChan chan *watchTab,
) {
	for {
		select {
		case <-ctx.Done():
			return
		case watchTab := <-watcherChan:
			for i, sv := range sourceValues {
				if watchTab.source == sv.source {
					sourceValues[i].value = watchTab.value
					break
				}
			}
			newInterface, stackErr := compose(t, sourceValues)
			if stackErr != nil {
				if d.params.OnWatchedError != nil {
					d.params.OnWatchedError(
						ctx, stackErr, d.value.Load(), newInterface)
				}
				continue
			}

			// Verify that the configuration is valid if a Verify() method is present.
			if vf, ok := newInterface.(VerifiedConfig); ok {
				if vfErr := vf.Verify(); vfErr != nil {
					if d.params.OnWatchedError != nil {
						d.params.OnWatchedError(
							ctx, vfErr, d.value.Load(), newInterface)
					}
					continue
				}
			}

			d.value.Store(newInterface)
			select {
			case d.updatesChan <- newInterface:
			default:
			}
		}
	}
}

func compose(t interface{}, sources []sourceValue) (interface{}, error) {
	copyValuePtr := realDeepCopy(t)
	value := copyValuePtr.Elem()
	for _, source := range sources {
		// automatically dereference pointers that may be in the value
		s := source.value
		if s.Kind() == reflect.Ptr {
			s = s.Elem()
		}
		o := newOverlayer()
		sv := o.dc.deepCopyValue(s)
		if overlayErr := o.overlayStruct(value, sv); overlayErr != nil {
			return nil, overlayErr
		}

	}

	return value.Addr().Interface(), nil
}

type sourceValue struct {
	source Source
	value  reflect.Value
}

// Type is a wrapper for a reflect.Type.
type Type struct {
	t reflect.Type
}

// Type describes a config struct type, usually it is already pointerified
func (t *Type) Type() reflect.Type {
	return t.t
}

// NewType constructs a new dials Type for a reflect.Type.
func NewType(t reflect.Type) *Type {
	return &Type{
		t: t,
	}
}
