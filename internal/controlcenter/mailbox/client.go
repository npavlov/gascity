package mailbox

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/gastownhall/gascity/internal/api/genclient"
)

const (
	defaultPageLimit = 50
	maximumPageLimit = 100
	readTimeout      = 3 * time.Second
)

type supervisorMailReader interface {
	GetV0CityByCityNameMailCountWithResponse(context.Context, string, *genclient.GetV0CityByCityNameMailCountParams, ...genclient.RequestEditorFn) (*genclient.GetV0CityByCityNameMailCountResponse, error)
	GetV0CityByCityNameMailWithResponse(context.Context, string, *genclient.GetV0CityByCityNameMailParams, ...genclient.RequestEditorFn) (*genclient.GetV0CityByCityNameMailResponse, error)
	GetV0CityByCityNameMailByIdWithResponse(context.Context, string, string, *genclient.GetV0CityByCityNameMailByIdParams, ...genclient.RequestEditorFn) (*genclient.GetV0CityByCityNameMailByIdResponse, error)
	GetV0CityByCityNameMailThreadByIdWithResponse(context.Context, string, string, *genclient.GetV0CityByCityNameMailThreadByIdParams, ...genclient.RequestEditorFn) (*genclient.GetV0CityByCityNameMailThreadByIdResponse, error)
}

// UpstreamError preserves stable mail operation and transport/protocol facts
// for the local Huma error mapper.
type UpstreamError struct {
	Code       string
	Operation  string
	StatusCode int
	Detail     string
	Err        error
}

func (e *UpstreamError) Error() string {
	if e == nil {
		return ""
	}
	if e.Detail != "" {
		return e.Operation + ": " + e.Detail
	}
	return e.Operation + ": " + e.Code
}

func (e *UpstreamError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

// Client is a stateless read-through adapter over exactly four generated GETs.
type Client struct {
	city string
	api  supervisorMailReader
}

// NewClient binds the single configured city to the narrow generated reader.
func NewClient(city string, api supervisorMailReader) (*Client, error) {
	if strings.TrimSpace(city) == "" {
		return nil, fmt.Errorf("control center mailbox: city name is required")
	}
	if api == nil {
		return nil, fmt.Errorf("control center mailbox: generated reader is required")
	}
	return &Client{city: city, api: api}, nil
}

// Count reads the authoritative city-global count without agent or rig scope.
func (c *Client) Count(ctx context.Context) (Count, error) {
	callCtx, cancel := context.WithTimeout(ctx, readTimeout)
	defer cancel()
	response, err := c.api.GetV0CityByCityNameMailCountWithResponse(callCtx, c.city, &genclient.GetV0CityByCityNameMailCountParams{})
	if problem := responseProblem("mail.count", response != nil, statusMailCount(response), problemMailCount(response), err); problem != nil {
		return Count{}, problem
	}
	if response.JSON200 == nil {
		return Count{}, protocolError("mail.count", http.StatusOK, "successful Supervisor response omitted its typed body", nil)
	}
	body := response.JSON200
	return ProjectCount(body.Total, body.Unread, boolValue(body.Partial), stringsValue(body.PartialErrors)), nil
}

// List reads exactly one bounded upstream page.
func (c *Client) List(ctx context.Context, query Query) (Page, error) {
	filter := query.Filter
	if filter == "" {
		filter = FilterUnread
	}
	if filter != FilterUnread && filter != FilterAll {
		return Page{}, protocolError("mail.list", 0, fmt.Sprintf("unsupported mail filter %q", filter), nil)
	}
	limit := query.Limit
	if limit <= 0 {
		limit = defaultPageLimit
	}
	if limit > maximumPageLimit {
		limit = maximumPageLimit
	}
	status := string(filter)
	params := &genclient.GetV0CityByCityNameMailParams{
		Status: &status,
		Cursor: optionalString(query.Cursor),
		Limit:  int64Ptr(int64(limit)),
	}
	callCtx, cancel := context.WithTimeout(ctx, readTimeout)
	defer cancel()
	response, err := c.api.GetV0CityByCityNameMailWithResponse(callCtx, c.city, params)
	if problem := responseProblem("mail.list", response != nil, statusMailList(response), problemMailList(response), err); problem != nil {
		return Page{}, problem
	}
	if response.JSON200 == nil {
		return Page{}, protocolError("mail.list", http.StatusOK, "successful Supervisor response omitted its typed body", nil)
	}
	body := response.JSON200
	items := generatedMessages(body.Items)
	page, projectErr := ProjectPage(items, body.Total, stringValue(body.NextCursor), boolValue(body.Partial), stringsValue(body.PartialErrors))
	if projectErr != nil {
		return Page{}, protocolError("mail.list", http.StatusOK, projectErr.Error(), projectErr)
	}
	return page, nil
}

// Get reads one selected message. The GET does not mutate its read bit.
func (c *Client) Get(ctx context.Context, id string, rig *string) (Message, error) {
	callCtx, cancel := context.WithTimeout(ctx, readTimeout)
	defer cancel()
	response, err := c.api.GetV0CityByCityNameMailByIdWithResponse(callCtx, c.city, id, &genclient.GetV0CityByCityNameMailByIdParams{Rig: rig})
	if problem := responseProblem("mail.detail", response != nil, statusMailDetail(response), problemMailDetail(response), err); problem != nil {
		return Message{}, problem
	}
	if response.JSON200 == nil {
		return Message{}, protocolError("mail.detail", http.StatusOK, "successful Supervisor response omitted its typed body", nil)
	}
	projected, projectErr := ProjectMessage(generatedMessage(*response.JSON200))
	if projectErr != nil {
		return Message{}, protocolError("mail.detail", http.StatusOK, projectErr.Error(), projectErr)
	}
	if projected.ID != id {
		return Message{}, protocolError("mail.detail", http.StatusOK, fmt.Sprintf("Supervisor returned message %q for requested message %q", projected.ID, id), nil)
	}
	return projected, nil
}

// Thread reads one ordered thread and forwards the optional provider hint.
func (c *Client) Thread(ctx context.Context, idOrThreadID string, rig *string) (Thread, error) {
	callCtx, cancel := context.WithTimeout(ctx, readTimeout)
	defer cancel()
	response, err := c.api.GetV0CityByCityNameMailThreadByIdWithResponse(callCtx, c.city, idOrThreadID, &genclient.GetV0CityByCityNameMailThreadByIdParams{Rig: rig})
	if problem := responseProblem("mail.thread", response != nil, statusMailThread(response), problemMailThread(response), err); problem != nil {
		return Thread{}, problem
	}
	if response.JSON200 == nil {
		return Thread{}, protocolError("mail.thread", http.StatusOK, "successful Supervisor response omitted its typed body", nil)
	}
	body := response.JSON200
	thread, projectErr := ProjectThread(generatedMessages(body.Items), body.Total, stringValue(body.NextCursor), boolValue(body.Partial), stringsValue(body.PartialErrors))
	if projectErr != nil {
		return Thread{}, protocolError("mail.thread", http.StatusOK, projectErr.Error(), projectErr)
	}
	return thread, nil
}

func responseProblem(operation string, responsePresent bool, status int, model *genclient.ErrorModel, err error) error {
	if err != nil {
		return &UpstreamError{Code: "upstream_unavailable", Operation: operation, Detail: "Supervisor mail request failed", Err: err}
	}
	if !responsePresent {
		return protocolError(operation, 0, "generated client returned a nil response", nil)
	}
	if status == http.StatusOK {
		return nil
	}
	code := "upstream_http"
	if status == http.StatusServiceUnavailable || status == http.StatusGatewayTimeout {
		code = "upstream_unavailable"
	}
	detail := http.StatusText(status)
	if model != nil && model.Detail != nil && strings.TrimSpace(*model.Detail) != "" {
		detail = *model.Detail
	}
	return &UpstreamError{Code: code, Operation: operation, StatusCode: status, Detail: detail}
}

func protocolError(operation string, status int, detail string, err error) error {
	return &UpstreamError{Code: "upstream_protocol", Operation: operation, StatusCode: status, Detail: detail, Err: err}
}

func generatedMessages(items *[]genclient.Message) []Message {
	if items == nil {
		return []Message{}
	}
	result := make([]Message, 0, len(*items))
	for _, item := range *items {
		result = append(result, generatedMessage(item))
	}
	return result
}

func generatedMessage(item genclient.Message) Message {
	return Message{
		ID:        item.Id,
		From:      item.From,
		To:        item.To,
		Subject:   item.Subject,
		Body:      item.Body,
		CreatedAt: item.CreatedAt,
		Read:      item.Read,
		ThreadID:  item.ThreadId,
		ReplyTo:   item.ReplyTo,
		Priority:  item.Priority,
		CC:        stringsValue(item.Cc),
		Rig:       item.Rig,
	}
}

func statusMailCount(response *genclient.GetV0CityByCityNameMailCountResponse) int {
	if response == nil {
		return 0
	}
	return response.StatusCode()
}

func problemMailCount(response *genclient.GetV0CityByCityNameMailCountResponse) *genclient.ErrorModel {
	if response == nil {
		return nil
	}
	return response.ApplicationproblemJSONDefault
}

func statusMailList(response *genclient.GetV0CityByCityNameMailResponse) int {
	if response == nil {
		return 0
	}
	return response.StatusCode()
}

func problemMailList(response *genclient.GetV0CityByCityNameMailResponse) *genclient.ErrorModel {
	if response == nil {
		return nil
	}
	return response.ApplicationproblemJSONDefault
}

func statusMailDetail(response *genclient.GetV0CityByCityNameMailByIdResponse) int {
	if response == nil {
		return 0
	}
	return response.StatusCode()
}

func problemMailDetail(response *genclient.GetV0CityByCityNameMailByIdResponse) *genclient.ErrorModel {
	if response == nil {
		return nil
	}
	return response.ApplicationproblemJSONDefault
}

func statusMailThread(response *genclient.GetV0CityByCityNameMailThreadByIdResponse) int {
	if response == nil {
		return 0
	}
	return response.StatusCode()
}

func problemMailThread(response *genclient.GetV0CityByCityNameMailThreadByIdResponse) *genclient.ErrorModel {
	if response == nil {
		return nil
	}
	return response.ApplicationproblemJSONDefault
}

func optionalString(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}

func stringValue(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func boolValue(value *bool) bool {
	return value != nil && *value
}

func stringsValue(value *[]string) []string {
	if value == nil {
		return []string{}
	}
	return append([]string{}, (*value)...)
}

func int64Ptr(value int64) *int64 { return &value }

var _ Reader = (*Client)(nil)
