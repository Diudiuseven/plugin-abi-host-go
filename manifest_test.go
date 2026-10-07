package pluginhost

import "testing"

func TestValidateManifestErrors(t *testing.T) {
	cases := []struct {
		name string
		m    Manifest
		want LoadErrorKind
	}{
		{"empty id", Manifest{ArtifactPath: "x", Generation: Gen1, Entries: []EntrySpec{{Name: "a", Output: KindString}}}, ErrManifestInvalid},
		{"empty artifact", Manifest{ModuleID: "m", Generation: Gen1, Entries: []EntrySpec{{Name: "a", Output: KindString}}}, ErrManifestInvalid},
		{"zero generation", Manifest{ModuleID: "m", ArtifactPath: "x", Entries: []EntrySpec{{Name: "a", Output: KindString}}}, ErrManifestInvalid},
		{"inverted range", Manifest{ModuleID: "m", ArtifactPath: "x", Generation: Gen2, MinHostGeneration: 3, MaxHostGeneration: 1, Entries: []EntrySpec{{Name: "a", Output: KindString}}}, ErrGenerationRange},
		{"no entries", Manifest{ModuleID: "m", ArtifactPath: "x", Generation: Gen1}, ErrManifestInvalid},
		{"unnamed entry", Manifest{ModuleID: "m", ArtifactPath: "x", Generation: Gen1, Entries: []EntrySpec{{Output: KindString}}}, ErrManifestInvalid},
		{"duplicate entry", Manifest{ModuleID: "m", ArtifactPath: "x", Generation: Gen1, Entries: []EntrySpec{
			{Name: "a", Output: KindString}, {Name: "a", Output: KindString},
		}}, ErrEntryDuplicate},
		{"bad output kind", Manifest{ModuleID: "m", ArtifactPath: "x", Generation: Gen1, Entries: []EntrySpec{{Name: "a", Output: "bogus"}}}, ErrEntryTypeMismatch},
		{"bad input kind", Manifest{ModuleID: "m", ArtifactPath: "x", Generation: Gen1, Entries: []EntrySpec{{Name: "a", Input: []Kind{"bogus"}, Output: KindString}}}, ErrEntryTypeMismatch},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			errs := ValidateManifest(tc.m)
			for _, e := range errs {
				if e.Kind == tc.want {
					if e.Module != "m" && tc.name != "empty id" {
						t.Fatalf("error not attributed to module: %+v", e)
					}
					return
				}
			}
			t.Fatalf("expected %s, got %v", tc.want, errs)
		})
	}
}

func TestValidateValidManifest(t *testing.T) {
	m := Manifest{
		ModuleID: "m", ArtifactPath: "x", Generation: Gen1,
		Entries: []EntrySpec{{Name: "a", Input: []Kind{KindString, KindInt}, Output: KindBool}},
	}
	if errs := ValidateManifest(m); len(errs) != 0 {
		t.Fatalf("valid manifest rejected: %v", errs)
	}
}
