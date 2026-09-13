package plan

import "testing"

func TestForKnownPlan(t *testing.T) {
	if got := For("iglesia"); got.Name != Iglesia {
		t.Fatalf("plan iglesia resolved to %q", got.Name)
	}
}

func TestUnknownPlanIsNotUnlimited(t *testing.T) {
	// A row written by a newer version, or by hand, must not become a church
	// with no ceiling. The failure that costs money is the permissive one.
	got := For("empresarial-pro-max")

	if got.Name != Free {
		t.Fatalf("unknown plan resolved to %q, want free", got.Name)
	}
	if got.Storage != all[Free].Storage {
		t.Fatalf("unknown plan got %d bytes of storage", got.Storage)
	}
}

func TestEmptyPlanIsFree(t *testing.T) {
	// Every organization created before the column existed reads as "".
	if got := For(""); got.Name != Free {
		t.Fatalf("missing plan resolved to %q, want free", got.Name)
	}
}

func TestPaidPlansAreLargerThanFree(t *testing.T) {
	free := For(string(Free))
	for _, name := range []Name{Iglesia, Red} {
		paid := For(string(name))
		if paid.Storage <= free.Storage {
			t.Errorf("%s gives %d bytes, no more than free's %d", name, paid.Storage, free.Storage)
		}
		if paid.MaxUpload <= free.MaxUpload {
			t.Errorf("%s caps files at %d, no more than free's %d", name, paid.MaxUpload, free.MaxUpload)
		}
	}
}

func TestEveryPlanCapsASingleFileBelowItsTotal(t *testing.T) {
	// One file that fills the whole plan is a plan that can hold exactly one
	// file, which is not a plan.
	for name, limits := range all {
		if limits.MaxUpload >= limits.Storage {
			t.Errorf("%s lets one file take the entire %d bytes", name, limits.Storage)
		}
	}
}

func TestHumanReadsLikeTheApp(t *testing.T) {
	cases := map[int64]string{
		512:       "0 KB",
		2 * MB:    "2 MB",
		25 * GB:   "25.0 GB",
		1536 * MB: "1.5 GB",
		// Rounded down, always. A plan one byte short of full must not read as
		// full, and room must never be reported larger than it is.
		500*MB - 512: "499 MB",
	}
	for bytes, want := range cases {
		if got := Human(bytes); got != want {
			t.Errorf("Human(%d) = %q, want %q", bytes, got, want)
		}
	}
}
