package main

func initOptions() {
	setErrExit = false
	setXTrace = false
	setPipefail = false
	setNoClobber = false
	setNoUnset = false
	setNoGlob = false
	setNotify = false
	setHistIgnoreDups = false
	setHistIgnoreSpace = false
	setHupOnExit = false
	setIgnoreEOF = false
	setHashAll = true
}

func applyConfigToOptions(cfg *Config) {
	setErrExit = cfg.ErrExit
	setXTrace = cfg.XTrace
	setPipefail = cfg.Pipefail
	setNoClobber = cfg.NoClobber
	setNoUnset = cfg.NoUnset
	setNoGlob = cfg.NoGlob
	setNotify = cfg.Notify
	setHistIgnoreDups = cfg.HistIgnoreDups
	setHistIgnoreSpace = cfg.HistIgnoreSpace
	setHupOnExit = cfg.HupOnExit
	setIgnoreEOF = cfg.IgnoreEOF
	setHashAll = cfg.HashAll
}
