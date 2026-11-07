package service

import (
	"context"
	"encoding/json"

	"valise/core/journey"
)

func (s *Service) agentReader() *AgentReader {
	s.readersMu.Lock()
	defer s.readersMu.Unlock()
	if s.agentsReader == nil {
		s.agentsReader = NewAgentReader(s)
	}
	return s.agentsReader
}

func (s *Service) ListAgents(ctx context.Context, req AgentListRequest) (AgentListResult, error) {
	return s.agentReader().ListAgents(ctx, req)
}
func (s *Service) GetAgent(ctx context.Context, req AgentGetRequest) (AgentObservation, error) {
	return s.agentReader().GetAgent(ctx, req)
}

// Changing repository registrations invalidates old fixed-view cursors. It does
// not turn an old view into a newly scanned list halfway through pagination.
func (s *Service) timelineReader() *JourneyReader {
	s.readersMu.Lock()
	defer s.readersMu.Unlock()
	b, _ := json.Marshal(s.Config.Repos)
	key := string(b)
	if s.journeyReader == nil || s.journeyReposDigest != key {
		s.journeyReader = NewJourneyReader(s.Config.Repos)
		s.journeyReposDigest = key
	}
	return s.journeyReader
}
func (s *Service) ListTimeline(ctx context.Context, req JourneyListRequest) (JourneyPage, error) {
	return s.timelineReader().ListTimeline(ctx, req)
}
func (s *Service) GetTimelinePoint(ctx context.Context, req JourneyPointRequest) (journey.TimelinePoint, error) {
	return s.timelineReader().GetTimelinePoint(ctx, req)
}
