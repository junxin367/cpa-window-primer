package main

const (
	pluginID           = "cpa-window-primer"
	pluginName         = "CPA Window Primer"
	pluginVersion      = "0.1.1"
	pluginAuthor       = "junxin367"
	pluginRepository   = "https://github.com/junxin367/cpa-window-primer"
	primerHeader       = "X-CPA-Window-Primer-Auth-ID"
	defaultModel       = "gpt-5.4"
	defaultPrompt      = "hi"
	defaultMinInterval = "5h"
	defaultTick        = "5s"
	defaultLead        = "1m"
)

var defaultTimes = []string{"07:00", "12:00", "17:00"}
