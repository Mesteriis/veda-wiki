package main

import "github.com/Mesteriis/veda-wiki/internal/transfer"

// Transfer names for the cmd tests that remain until their areas move.

type moveRequest = transfer.MoveRequest

func (s *Store) copyArticle(in copyRequest) (Revision, error) { return s.transfer().Copy(in) }

func (s *Store) moveArticle(in moveRequest) (Move, error) { return s.transfer().Move(in) }

func (s *Store) moves() []Move { return s.transfer().Moves() }

func (s *Store) cancelMove(id string, confirm bool) error {
	return s.transfer().CancelMove(id, confirm)
}

func (s *Store) planTransfer(in copyRequest) (transfer.Plan, error) {
	return s.transfer().PlanTransfer(in)
}

func (s *Store) verifyAsset(ref BlobRef) error { return s.VerifyBlob(ref) }

// transferPlan decodes a transfer plan response with the agent-context
// source statuses that Store.transfer reports.
type transferPlan struct {
	Articles          []transfer.PlanItem `json:"articles"`
	SourceStatus      contextStatus       `json:"source_status"`
	DestinationStatus contextStatus       `json:"destination_status"`
}
