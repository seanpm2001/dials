package dials

import (
	"context"
	"fmt"
	"io"
	"reflect"
	"sync/atomic"

	"github.com/vimeo/dials/ptrify"
)

// Config ...
func Config(ctx context.Context, t interface{}, sources ...Source) (*View, error) {

	watcherChan := make(chan *watchTab)
	computed := make([]sourceValue, 0, len(sources))

	typeOfT := reflect.TypeOf(t)
	if typeOfT.Kind() != reflect.Ptr {
		return nil, fmt.Errorf("config type %T is not a pointer", t)
	}

	tVal := reflect.ValueOf(t)

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

	newValue, err := compose(t, computed)
	if err != nil {
		return nil, err
	}

	view := &View{
		value:       atomic.Value{},
		updatesChan: make(chan interface{}, 1),
	}
	view.value.Store(newValue)

	if someoneWatching {
		go view.monitor(ctx, t, computed, watcherChan)
	}
	return view, nil
}

// Source ...
type Source interface {
	Value(*Type) (reflect.Value, error)
}

// Decoder ...
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

type View struct {
	value       atomic.Value
	updatesChan chan interface{}
}

// Get returns the configuration struct populated.
func (v *View) Get() interface{} {
	return v.value.Load()
}

func (v *View) Events() <-chan interface{} {
	return v.updatesChan
}

func (v *View) monitor(
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
			newInterface, err := compose(t, sourceValues)
			if err != nil {
				continue
			}
			v.value.Store(newInterface)
			select {
			case v.updatesChan <- newInterface:
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
		if overlayErr := overlayStruct(value, s); overlayErr != nil {
			return nil, overlayErr
		}

	}

	return value.Addr().Interface(), nil
}

type sourceValue struct {
	source Source
	value  reflect.Value
}

type Type struct {
	t reflect.Type
}

// Builder returns a builder
func (t *Type) Builder() *Builder {
	reflectType := t.Type()
	newValue := reflect.New(t.Type()).Elem()
	fields := make([]*Field, 0, reflectType.NumField())
	for i := 0; i < reflectType.NumField(); i++ {
		f := &Field{
			Name:  []string{reflectType.Field(i).Name},
			Value: newValue.Field(i),
		}
		fields = append(fields, f)
	}

	return &Builder{
		t:      t,
		fields: fields,
		value:  newValue,
	}
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

// Builder is a struct with some convenience methods.
type Builder struct {
	t      *Type
	fields []*Field
	value  reflect.Value
}

func (b *Builder) Lookup(name ...string) (*Field, error) {
	return nil, nil
}

func (b *Builder) Fields() []*Field {
	return b.fields
}

func (b *Builder) Finish() (reflect.Value, error) {

	// for _, f := range b.fields {
	// 	name := f.Name[0]
	// 	fieldToSet := newValue.FieldByName(name)
	// 	if fieldToSet.CanSet() {
	// 		nv := reflect.New(fieldToSet.Type().Elem())
	// 		nv.Elem().Set(f.Value)
	// 		fieldToSet.Set(nv)
	// 	} else {
	// 		return reflect.Value{}, fmt.Errorf("can't set field %s", name)
	// 	}
	//
	// }
	return b.value, nil
}

type Field struct {
	Name  []string
	Value reflect.Value
}

func (f *Field) Set(newVal string) {
	nv := reflect.New(f.Value.Type().Elem())
	nv.Elem().Set(reflect.ValueOf(newVal))

	f.Value.Set(nv)
}
