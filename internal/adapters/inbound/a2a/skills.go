package a2a

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2asrv"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"

	"github.com/claudioed/node-performance/internal/application/ports"
	"github.com/claudioed/node-performance/internal/application/usecases"
	"github.com/claudioed/node-performance/internal/domain/shared"
)

// tracerName is the OTel instrumentation scope for A2A skill spans, mirroring
// the MCP adapter's mcp.tool spans.
const tracerName = "github.com/claudioed/node-performance/internal/adapters/inbound/a2a"

// Skill ids advertised on the Agent Card (see Card). Each is the goal of one
// MCP read tool (ADR 0007): node_performance is get_scorecard and
// nodes_by_grade is list_scorecards_by_grade.
const (
	skillNodePerformance = "node_performance"
	skillNodesByGrade    = "nodes_by_grade"
)

// skillRequest is the wire contract of every skill call: the first meaningful
// message part carries (as a Data part, or a Text part holding the same JSON)
// the skill id and its arguments. Strict on purpose: an agent client is
// machine-to-machine, so unknown fields are rejected, never ignored, and a
// misspelled name like "windw" must not silently widen the read to both
// windows. (Go's encoding/json matches field names case-insensitively, so
// "window" spelled "WINDOW" still reaches window; only genuinely unknown names
// are refused.)
type skillRequest struct {
	Skill     string          `json:"skill"`
	Arguments json.RawMessage `json:"arguments"`
}

// nodePerformanceArgs and nodesByGradeArgs are the per-skill argument shapes,
// the same inputs as the MCP tools (the optional ones carry omitempty there
// for the schema; here the card documents them).
type nodePerformanceArgs struct {
	NodeID string `json:"nodeId"`
}

type nodesByGradeArgs struct {
	Grade  string `json:"grade"`
	Window string `json:"window"`
	Limit  int    `json:"limit"`
	After  string `json:"after"`
}

// answer maps one incoming message to a skill and returns the agent's reply
// Message. It is the whole executor body: this is a message-only agent, so a
// skill run is exactly one request mapped to one answer (ADR 0011).
func answer(ctx context.Context, deps Deps, ec *a2asrv.ExecutorContext) a2a.Event {
	req, problem := parseSkillRequest(ec)
	if problem != nil {
		return reply(ec, envelope{Skill: "", Problem: problem}, problem.summary())
	}

	ctx, span := otel.Tracer(tracerName).Start(ctx, "a2a.skill "+req.Skill)
	defer span.End()
	span.SetAttributes(attribute.String("a2a.skill", req.Skill))

	dto, problem := run(ctx, deps, req)
	if problem != nil {
		span.SetStatus(codes.Error, problem.Detail)
		return reply(ec, envelope{Skill: req.Skill, Problem: problem}, problem.summary())
	}
	return reply(ec, envelope{Skill: req.Skill, Data: dto}, dto.Summary())
}

// run dispatches one validated request to its skill.
func run(ctx context.Context, deps Deps, req skillRequest) (skillDTO, *problem) {
	switch req.Skill {
	case skillNodePerformance:
		data, problem := deps.nodePerformance(ctx, req.Arguments)
		if problem != nil {
			return skillDTO{}, problem
		}
		return skillDTO{NodePerformance: &data, summary: summarizeNode(data)}, nil
	case skillNodesByGrade:
		data, grade, problem := deps.nodesByGrade(ctx, req.Arguments)
		if problem != nil {
			return skillDTO{}, problem
		}
		return skillDTO{NodesByGrade: &data, summary: summarizePage(grade, data)}, nil
	default:
		return skillDTO{}, problemUnknownSkill(req.Skill)
	}
}

// reply builds the agent Message for one skill run: a Data part carrying the
// structured envelope and a Text part carrying the one-line summary, bound to
// the caller's context (and task, when the client referenced one).
func reply(ec *a2asrv.ExecutorContext, env envelope, summary string) a2a.Event {
	return a2a.NewMessageForTask(a2a.MessageRoleAgent, ec, a2a.NewDataPart(env), a2a.NewTextPart(summary))
}

// parseSkillRequest extracts the skillRequest from the message's first
// meaningful part. A Data part (or a Text part holding the same JSON object)
// is accepted; anything else is a request-shape problem, answered, not raised,
// because a malformed delegation is a business answer for the calling agent,
// not a transport failure.
func parseSkillRequest(ec *a2asrv.ExecutorContext) (skillRequest, *problem) {
	msg := ec.Message
	if msg == nil || len(msg.Parts) == 0 {
		return skillRequest{}, problemMalformed("the message carries no parts")
	}
	raw, err := partJSON(msg.Parts[0])
	if err != nil {
		return skillRequest{}, problemMalformed(fmt.Sprintf("the first message part is not a skill request: %v", err))
	}
	var req skillRequest
	if err := decodeStrict(raw, &req); err != nil {
		return skillRequest{}, problemMalformed(fmt.Sprintf("the skill request is not valid: %v", err))
	}
	if req.Skill == "" {
		return skillRequest{}, problemMalformed(`the skill request carries no "skill" field`)
	}
	return req, nil
}

// partJSON renders one part's content as the JSON bytes of a skill request.
func partJSON(part *a2a.Part) ([]byte, error) {
	switch content := part.Content.(type) {
	case a2a.Data:
		return json.Marshal(content.Value)
	case a2a.Text:
		return []byte(string(content)), nil
	default:
		return nil, errors.New("expected a data part (or a text part holding the skill JSON)")
	}
}

// decodeStrict decodes one JSON document into dst, rejecting unknown fields
// and trailing data. Absent arguments decode as the empty object so each
// skill's own use-case validation produces the specific problem.
func decodeStrict(raw []byte, dst any) error {
	if len(bytes.TrimSpace(raw)) == 0 {
		raw = []byte("{}")
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return err
	}
	if dec.More() {
		return errors.New("unexpected data after the JSON document")
	}
	return nil
}

// nodePerformance is the single-node read skill, through the same
// GetScorecard use case the REST and MCP adapters use.
func (d Deps) nodePerformance(ctx context.Context, rawArgs json.RawMessage) (nodeScorecardsView, *problem) {
	var args nodePerformanceArgs
	if err := decodeStrict(rawArgs, &args); err != nil {
		return nodeScorecardsView{}, problemMalformed(fmt.Sprintf("arguments: %v", err))
	}
	got, err := d.GetScorecard.GetScorecard(ctx, args.NodeID)
	if err != nil {
		return nodeScorecardsView{}, problemFromUseCase(err, args.NodeID)
	}
	return toNodeScorecards(got), nil
}

// nodesByGrade is the grade-roster read skill, through the same
// ListScorecardsByGrade use case the REST and MCP adapters use. It also
// returns the requested grade for the summary line.
func (d Deps) nodesByGrade(ctx context.Context, rawArgs json.RawMessage) (scorecardPageView, string, *problem) {
	var args nodesByGradeArgs
	if err := decodeStrict(rawArgs, &args); err != nil {
		return scorecardPageView{}, "", problemMalformed(fmt.Sprintf("arguments: %v", err))
	}
	page, err := d.ListScorecards.ListScorecardsByGrade(ctx, usecases.ListByGradeInput{
		Grade: args.Grade, Window: args.Window, Limit: args.Limit, After: args.After,
	})
	if err != nil {
		return scorecardPageView{}, "", problemFromUseCase(err, "")
	}
	return toScorecardPage(page), args.Grade, nil
}

// problemFromUseCase maps a use case error to a problem answer, using the SAME
// classification as the REST adapter (internal/adapters/inbound/http
// problemCatalogue) for the errors a read can raise: a node nothing was
// observed about is 404 not-found, a value the domain refuses is 422
// validation-failed and anything unrecognised is 500 internal-error. (The REST
// 409 concurrent-update belongs to a write path this read-only agent does not
// offer, and 503 dependency-unavailable is the readiness probe's.) The raw
// text of an unrecognised error, which may carry infrastructure detail, is
// logged and never echoed. Domain rejections are EXPECTED business outcomes
// and are answered as RFC 7807-shaped problems; anything unrecognised is still
// answered as a problem, so the calling agent always receives a structured,
// machine-readable result.
func problemFromUseCase(err error, nodeID string) *problem {
	switch {
	case errors.Is(err, ports.ErrNotFound):
		return &problem{Type: "not-found", Title: "Not found", Detail: fmt.Sprintf("no scorecard exists for node %q", nodeID), Status: http.StatusNotFound}
	case errors.Is(err, shared.ErrValidation):
		return &problem{Type: "validation-failed", Title: "Validation failed", Detail: err.Error(), Status: http.StatusUnprocessableEntity}
	default:
		slog.Error("a2a skill failed", "error", err)
		return &problem{Type: "internal-error", Title: "Internal server error", Detail: "the skill failed; see server logs (trace id in the response metadata)", Status: http.StatusInternalServerError}
	}
}
