package panels

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vimeo/dials"
	"github.com/vimeo/dials/decoders/json"
	"github.com/vimeo/dials/sources/static"
)

type testSubPanel[RT, T any] struct {
	t              testing.TB
	sp             SetupParams[T]
	dCfg           *T
	cmdArgs        []string
	expectedSCPath []string
	expectedArgs   []string
	expSubDCfg     *T
	expPanelDCfg   *RT
}

func (t *testSubPanel[RT, T]) Description(scPath []string) string {
	return "description " + strings.Join(scPath, "-")
}

func (t *testSubPanel[RT, T]) ShortUsage(scPath []string) string {
	return "short " + strings.Join(scPath, "-")
}

func (t *testSubPanel[RT, T]) LongUsage(scPath []string) string {
	return "long " + strings.Join(scPath, "-")
}

func (t *testSubPanel[RT, T]) DefaultConfig() *T {
	return t.dCfg
}

func (t *testSubPanel[RT, T]) SetupParams() SetupParams[T] {
	return t.sp
}

func (t *testSubPanel[RT, T]) Run(ctx context.Context, s *Handle) error {
	t.t.Logf("args: %q, %q, %q", s.Args, s.CommandArgs, s.SCPath)
	assert.Equal(t.t, t.cmdArgs, s.CommandArgs)
	assert.Equal(t.t, t.expectedArgs, s.Args)
	assert.Equal(t.t, t.expectedSCPath, s.SCPath)
	assert.Equal(t.t, t.expSubDCfg, s.Dials.View())

	if t.expPanelDCfg != nil {
		assert.Equal(t.t, t.expPanelDCfg, s.DialsPath[0].View())
	}

	assert.Equal(t.t, s.Dials, s.DialsPath[1])

	return nil
}

func TestPanels(t *testing.T) {
	testCases := []struct {
		name   string
		p      Panel
		tsp    testSubPanel
		scName string
	}{
		{
			name:   "multiple_subcommands_with_flags",
			p:      Panel{},
			scName: "testSubPanel",
			tsp: testSubPanel{
				t: t,
				dCfg: &struct {
					Fibble string
					Fabble string
				}{
					Fibble: "food",
					Fabble: "pizza",
				},
				expSubDCfg: &struct {
					Fibble string
					Fabble string
				}{
					Fibble: "bar",
					Fabble: "foo",
				},
				sp:             SetupParams{},
				cmdArgs:        []string{"hello", "testSubPanel", "--fibble=bar", "--fabble=foo", "beThere"},
				expectedSCPath: []string{"hello", "testSubPanel"},
				expectedArgs:   []string{"beThere"},
			},
		},
		{
			name: "NewDialsFuncPanel",
			p: Panel[struct {
				Song   string
				Artist string
			}]{
				dCfg: &struct {
					Song   string
					Artist string
				}{
					Song:   "yellow submarine",
					Artist: "The Beatles",
				},
				sp: SetupParams{
					NewDials: func(ctx context.Context, defaultCfg interface{}, flagsSource dials.Source) (*dials.Dials, error) {
						return dials.Config(ctx, defaultCfg, flagsSource, &static.StringSource{
							Decoder: &json.Decoder{},
							Data:    `{"Song":"Hello Goodbye"}`,
						})
					},
				},
			},
			scName: "testSubPanel",
			tsp: testSubPanel{
				t: t,
				dCfg: &struct {
					Fibble string
					Fabble string
				}{
					Fibble: "food",
					Fabble: "pizza",
				},
				expSubDCfg: &struct {
					Fibble string
					Fabble string
				}{
					Fibble: "bar",
					Fabble: "foo",
				},
				sp:             SetupParams{},
				cmdArgs:        []string{"hello", "testSubPanel", "--fibble=bar", "--fabble=foo", "beThere"},
				expectedSCPath: []string{"hello", "testSubPanel"},
				expectedArgs:   []string{"beThere"},
				expPanelDCfg: &struct {
					Song   string
					Artist string
				}{
					Song:   "Hello Goodbye",
					Artist: "The Beatles",
				},
			},
		},
		{
			name:   "NewDialsFuncSubPanel",
			p:      Panel{},
			scName: "testSubPanel",
			tsp: testSubPanel{
				t: t,
				dCfg: &struct {
					Fibble string
					Fabble string
				}{
					Fibble: "food",
					Fabble: "pizza",
				},
				expSubDCfg: &struct {
					Fibble string
					Fabble string
				}{
					Fibble: "bar",
					Fabble: "foo",
				},
				sp: SetupParams{
					NewDials: func(ctx context.Context, defaultCfg interface{}, flagsSource dials.Source) (*dials.Dials, error) {
						return dials.Config(ctx, defaultCfg, flagsSource, &static.StringSource{
							Decoder: &json.Decoder{},
							Data:    `{"Fabble":"foo"}`,
						})
					},
				},
				cmdArgs:        []string{"hello", "testSubPanel", "--fibble=bar", "beThere"},
				expectedSCPath: []string{"hello", "testSubPanel"},
				expectedArgs:   []string{"beThere"},
			},
		},
	}

	for _, testCase := range testCases {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		tc := testCase
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			sch, regErr := Register(tc.p, tc.scName, &tc.tsp)
			require.NoError(t, regErr)
			require.NotNil(t, sch)
			assert.Len(t, tc.p.schMap, 1)

			runErr := tc.p.Run(ctx, tc.tsp.cmdArgs)
			assert.NoError(t, runErr)
		})
	}
}
