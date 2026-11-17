package prof

import (
	"testing"

	"github.com/google/pprof/profile"
)

// Profil synthétique : main appelle work (ligne 10) et helper (ligne 11) ;
// work appelle helper (ligne 21), et helper est en partie intégré (inlining)
// dans work (ligne 22).
func sample() *profile.Profile {
	fMain := &profile.Function{ID: 1, Name: "ex/app.main", Filename: "/src/main.go", StartLine: 5}
	fWork := &profile.Function{ID: 2, Name: "ex/app.work", Filename: "/src/work.go", StartLine: 20}
	fHelp := &profile.Function{ID: 3, Name: "ex/app.helper", Filename: "/src/help.go", StartLine: 30}
	loc := func(id uint64, lines ...profile.Line) *profile.Location {
		return &profile.Location{ID: id, Line: lines}
	}
	main10 := loc(1, profile.Line{Function: fMain, Line: 10})
	main11 := loc(2, profile.Line{Function: fMain, Line: 11})
	work21 := loc(3, profile.Line{Function: fWork, Line: 21})
	help31 := loc(4, profile.Line{Function: fHelp, Line: 31})
	// helper intégré dans work : la première ligne est la fonction intégrée.
	inlined := loc(5, profile.Line{Function: fHelp, Line: 32}, profile.Line{Function: fWork, Line: 22})
	work23 := loc(6, profile.Line{Function: fWork, Line: 23})

	return &profile.Profile{
		SampleType: []*profile.ValueType{{Type: "samples", Unit: "count"}, {Type: "cpu", Unit: "nanoseconds"}},
		Sample: []*profile.Sample{
			{Location: []*profile.Location{help31, work21, main10}, Value: []int64{3, 300}},
			{Location: []*profile.Location{work23, main10}, Value: []int64{5, 500}},
			{Location: []*profile.Location{inlined, main10}, Value: []int64{1, 100}},
			{Location: []*profile.Location{help31, main11}, Value: []int64{1, 100}},
		},
		Function:      []*profile.Function{fMain, fWork, fHelp},
		Location:      []*profile.Location{main10, main11, work21, help31, inlined, work23},
		DurationNanos: 2e9,
	}
}

func find(r *Report, name string) Func {
	for _, f := range r.Funcs {
		if f.Name == name {
			return f
		}
	}
	return Func{}
}

func TestAnalyze(t *testing.T) {
	r, err := Analyze(sample(), "")
	if err != nil {
		t.Fatal(err)
	}
	if r.Kind != "cpu" || r.Unit != "nanoseconds" || r.Total != 1000 || r.Duration.Seconds() != 2 {
		t.Fatalf("rapport : %s %s total=%d durée=%v", r.Kind, r.Unit, r.Total, r.Duration)
	}
	want := map[string][2]int64{ // flat, cum
		"ex/app.work":   {500, 900},
		"ex/app.helper": {500, 500}, // 300 + 100 (intégré) + 100
		"ex/app.main":   {0, 1000},
	}
	for name, w := range want {
		f := find(r, name)
		if f.Flat != w[0] || f.Cum != w[1] {
			t.Errorf("%s : flat=%d cum=%d, attendu %v", name, f.Flat, f.Cum, w)
		}
	}
	// Tri par coût propre puis par nom ; puis par coût cumulé.
	if r.Funcs[0].Name != "ex/app.helper" || r.Funcs[2].Name != "ex/app.main" {
		t.Errorf("tri flat : %v", r.Funcs)
	}
	r.Sort(true)
	if r.Funcs[0].Name != "ex/app.main" {
		t.Errorf("tri cum : %v", r.Funcs)
	}
	if find(r, "ex/app.work").File != "/src/work.go" || find(r, "ex/app.work").Line != 20 {
		t.Error("emplacement de la fonction perdu")
	}
	if r.Format(1500) != "1.5µs" || r.Format(2_500_000_000) != "2.50s" || r.Percent(250) != 25 {
		t.Errorf("format : %s %s %.0f", r.Format(1500), r.Format(2_500_000_000), r.Percent(250))
	}
}

func TestLines(t *testing.T) {
	r, _ := Analyze(sample(), "")
	got := r.Lines("ex/app.work")
	want := []LineCost{{Line: 21, Flat: 0, Cum: 300}, {Line: 22, Flat: 0, Cum: 100}, {Line: 23, Flat: 500, Cum: 500}}
	if len(got) != len(want) {
		t.Fatalf("lignes : %+v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("ligne %d : %+v, attendu %+v", want[i].Line, got[i], want[i])
		}
	}
}

func TestSampleType(t *testing.T) {
	r, _ := Analyze(sample(), "samples")
	if r.Kind != "samples" || r.Total != 10 || r.Format(10) != "10" {
		t.Errorf("type samples : %s total=%d", r.Kind, r.Total)
	}
	if ShortName("github.com/x/y/pkg.(*T).M") != "pkg.(*T).M" {
		t.Error("ShortName")
	}
}
