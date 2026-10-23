package server

import "fmt"

// LOCAL_MODE, the boot half.

// bootPlan is what Initialize does at boot.
type bootPlan struct {
	Local   bool
	Migrate bool
	Tickers bool
}

// planBoot answers the boot plan for a process.
func planBoot(localMode bool) bootPlan {
	if !localMode {
		return bootPlan{Migrate: true, Tickers: true}
	}
	return bootPlan{Local: true, Migrate: true}
}

// localBanner is the one line a local run logs at boot naming what is off.
func localBanner() string {
	return "LOCAL_MODE on: database via loopback, schema ensured on the UAT database; OFF: abandoned-signup sweep, " +
		"exploring-tenant activation (the UAT pods run them)"
}

// String keeps a plan readable in a failed test.
func (p bootPlan) String() string {
	return fmt.Sprintf("local=%t migrate=%t tickers=%t", p.Local, p.Migrate, p.Tickers)
}
