package server

import (
	"strings"
	"testing"
)

func TestPlanBootDeployedDoesEverything(t *testing.T) {
	want := bootPlan{Migrate: true, Tickers: true}
	if got := planBoot(false); got != want {
		t.Fatalf("deployed plan = %+v, want %+v", got, want)
	}
}

func TestPlanBootLocalMigratesAndSwitchesOffTickers(t *testing.T) {
	p := planBoot(true)
	if !p.Migrate {
		t.Fatalf("local plan %+v: want Migrate", p)
	}
	if p.Tickers {
		t.Fatalf("local plan %+v leaves a ticker on", p)
	}
}

func TestLocalBannerNamesWhatIsOff(t *testing.T) {
	b := localBanner()
	for _, want := range []string{"LOCAL_MODE on", "sweep", "activation"} {
		if !strings.Contains(b, want) {
			t.Errorf("banner %q does not name %q", b, want)
		}
	}
}
