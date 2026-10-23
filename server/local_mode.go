package server

// bootPlan is what Initialize does at boot.
type bootPlan struct {
	Migrate bool
	Tickers bool
}

// planBoot answers the boot plan for a process.
func planBoot(localMode bool) bootPlan {
	if !localMode {
		return bootPlan{Migrate: true, Tickers: true}
	}
	return bootPlan{Migrate: true}
}

// localBanner is the one line a local run logs at boot naming what is off.
func localBanner() string {
	return "LOCAL_MODE on: database via loopback, schema ensured on the UAT database; OFF: abandoned-signup sweep, " +
		"exploring-tenant activation (the UAT pods run them)"
}
