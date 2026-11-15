package vaultrpc

import "valise/core/journey"

const JourneyCapability = "journey/1"
const MethodJourney = "journey_list"

type JourneyParams struct {
	Target string `json:"target,omitempty"`
}

type JourneyResult struct {
	ServerName string         `json:"server_name"`
	Targets    []string       `json:"targets,omitempty"`
	Result     journey.Result `json:"result"`
}
