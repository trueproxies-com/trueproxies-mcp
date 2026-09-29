package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	scopeServices = "services:read"
	scopeBilling  = "billing:read"
	scopeProxy    = "proxy:read"

	msgNoKey        = "This tool needs a TrueProxies API key. Create one in the dashboard under API keys."
	msgBadServiceID = "service_id must be a service ID (a UUID) from list_services."
	msgBadInvoiceID = "invoice_id must be an invoice ID (a UUID) from list_invoices."
)

var uuidPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// authFunc returns the Authorization header value for a tool call, or "".
type authFunc func(*mcp.CallToolRequest) string

type tools struct {
	api  *apiClient
	auth authFunc
}

func newServer(api *apiClient, auth authFunc) *mcp.Server {
	s := mcp.NewServer(&mcp.Implementation{
		Name:        "trueproxies",
		Title:       "TrueProxies",
		Description: "Read your TrueProxies account: plans, services, usage, invoices, endpoints and connection checks.",
		Version:     version,
		WebsiteURL:  "https://trueproxies.com",
	}, nil)
	t := &tools{api: api, auth: auth}
	t.register(s)
	return s
}

// tool describes a tool that changes nothing on the account. readOnly is false
// for tools that use the service itself, and openWorld marks tools that reach
// outside the customer's own account data.
func tool(name, title, description string, readOnly, openWorld bool) *mcp.Tool {
	no := false
	return &mcp.Tool{
		Name:        name,
		Title:       title,
		Description: description,
		Annotations: &mcp.ToolAnnotations{
			Title:           title,
			ReadOnlyHint:    readOnly,
			DestructiveHint: &no,
			OpenWorldHint:   &openWorld,
		},
	}
}

type plansIn struct {
	Product string `json:"product,omitempty" jsonschema:"Only this product: datacenter_ipv6, residential_ipv4_unlimited or residential_ipv4_gb"`
	Term    string `json:"term,omitempty" jsonschema:"Only this billing term: hour, day, week or month. Traffic packs have no term."`
}

type serviceIn struct {
	ServiceID string `json:"service_id" jsonschema:"Service ID (a UUID) from list_services"`
}

type listServicesIn struct {
	View   string `json:"view,omitempty" jsonschema:"current (default) lists every service that is not closed; history lists closed services; all lists both"`
	Cursor string `json:"cursor,omitempty" jsonschema:"Cursor from the previous page"`
	Limit  int    `json:"limit,omitempty" jsonschema:"Page size"`
}

type usageHistoryIn struct {
	ServiceID string `json:"service_id,omitempty" jsonschema:"Service ID (a UUID) from list_services. Leave empty for the whole account."`
	Range     string `json:"range,omitempty" jsonschema:"24h, 7d or 30d"`
}

type listInvoicesIn struct {
	ServiceID string `json:"service_id,omitempty" jsonschema:"Only invoices for this service ID (a UUID)"`
	Status    string `json:"status,omitempty" jsonschema:"open, paid or cancelled"`
	Kind      string `json:"kind,omitempty" jsonschema:"purchase, renewal, topup, custom, wallet_topup or white_label"`
	Cursor    string `json:"cursor,omitempty" jsonschema:"Cursor from the previous page"`
	Limit     int    `json:"limit,omitempty" jsonschema:"Page size"`
}

type invoiceIn struct {
	InvoiceID string `json:"invoice_id" jsonschema:"Invoice ID (a UUID) from list_invoices"`
}

type checkIn struct {
	ServiceID string `json:"service_id" jsonschema:"Service ID (a UUID) from list_services"`
	Protocol  string `json:"protocol,omitempty" jsonschema:"http, https or socks5"`
	Country   string `json:"country,omitempty" jsonschema:"Two-letter country code, if the service supports country targeting"`
	City      string `json:"city,omitempty" jsonschema:"City in lowercase letters, digits and underscores, if the service supports city targeting"`
	Region    string `json:"region,omitempty" jsonschema:"Region code, if the service supports region targeting"`
	ASN       string `json:"asn,omitempty" jsonschema:"ASN digits without the AS prefix, if the service supports ASN targeting"`
	Session   string `json:"session,omitempty" jsonschema:"Session type. sticky (Sticky) reuses an IP while available, for the session lifetime. rotate (Rotating) requests a new IP per connection, and the same IP can appear again. none uses the service default."`
	Lifetime  int    `json:"lifetime,omitempty" jsonschema:"Sticky session lifetime in seconds, 60 to 86400, only with session sticky"`
}

type endpointsIn struct {
	ServiceID string `json:"service_id" jsonschema:"Service ID (a UUID) from list_services"`
	Protocol  string `json:"protocol,omitempty" jsonschema:"http, https or socks5"`
	Format    string `json:"format,omitempty" jsonschema:"Line format: host:port:user:pass (default), user:pass@host:port, user:pass, host:port, url or curl"`
	Count     int    `json:"count,omitempty" jsonschema:"With session sticky, how many sticky sessions to return, 1 to 1000. Other session types return one line."`
	Country   string `json:"country,omitempty" jsonschema:"Two-letter country code, if the service supports country targeting"`
	City      string `json:"city,omitempty" jsonschema:"City in lowercase letters, digits and underscores, if the service supports city targeting"`
	Region    string `json:"region,omitempty" jsonschema:"Region code, if the service supports region targeting"`
	ASN       string `json:"asn,omitempty" jsonschema:"ASN digits without the AS prefix, if the service supports ASN targeting"`
	Session   string `json:"session,omitempty" jsonschema:"Session type. sticky (Sticky) reuses an IP while available, for the session lifetime. rotate (Rotating) requests a new IP per connection, and the same IP can appear again. none uses the service default."`
	Lifetime  int    `json:"lifetime,omitempty" jsonschema:"Sticky session lifetime in seconds, 60 to 86400, only with session sticky"`
}

func (t *tools) register(s *mcp.Server) {
	mcp.AddTool(s, tool("list_plans", "List plans and prices",
		"Lists TrueProxies plans and traffic packs: price in US cents, term, speed, connection limits, traffic allowance and targeting capabilities. Also returns the cities available for city targeting and whether the free trial is open. Traffic packs (residential_ipv4_gb) have no term; their traffic is valid for 30 days. No API key needed.",
		true, false), t.listPlans)

	mcp.AddTool(s, tool("get_account", "Get your account",
		"Returns the TrueProxies account that owns the API key: account reference, email, role, status and billing details.",
		true, false), func(ctx context.Context, req *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, any, error) {
		return t.call(ctx, req, "get_account", scopeServices, http.MethodGet, "/v1/me", nil, nil)
	})

	mcp.AddTool(s, tool("list_services", "List your services",
		"Lists the services on the account with status, product, plan, expiry and connection hosts. Paginated: pass next_cursor from the previous response as cursor.",
		true, false), func(ctx context.Context, req *mcp.CallToolRequest, in listServicesIn) (*mcp.CallToolResult, any, error) {
		q := query("view", in.View, "cursor", in.Cursor, "limit", num(in.Limit))
		return t.call(ctx, req, "list_services", scopeServices, http.MethodGet, "/v1/services", q, nil)
	})

	mcp.AddTool(s, tool("get_service", "Get a service",
		"Returns one service: status, expiry, connection hosts and ports, capabilities (the targeting and session options it supports) and effective limits. Does not return the proxy password.",
		true, false), func(ctx context.Context, req *mcp.CallToolRequest, in serviceIn) (*mcp.CallToolResult, any, error) {
		if !uuidPattern.MatchString(in.ServiceID) {
			return fail(msgBadServiceID)
		}
		return t.call(ctx, req, "get_service", scopeServices, http.MethodGet, "/v1/services/"+in.ServiceID, nil, nil)
	})

	mcp.AddTool(s, tool("get_traffic_used", "Get traffic used and remaining",
		"Returns traffic for one service, in bytes: bytes_out is downloaded and bytes_in is uploaded since the service started, remaining_bytes is traffic remaining and allowance_bytes is the allowance. traffic_batches lists traffic packs with their expiry. Traffic can take about a minute to appear.",
		true, false), func(ctx context.Context, req *mcp.CallToolRequest, in serviceIn) (*mcp.CallToolResult, any, error) {
		if !uuidPattern.MatchString(in.ServiceID) {
			return fail(msgBadServiceID)
		}
		res, _, _ := t.call(ctx, req, "get_traffic_used", scopeServices, http.MethodGet, "/v1/services/"+in.ServiceID+"/usage", nil, nil)
		return without(res, providerFields...), nil, nil
	})

	mcp.AddTool(s, tool("get_live_traffic", "Get live traffic",
		"Returns live traffic for one service: download and upload speed in bits per second over the last ten seconds, traffic used today (UTC) and recent connection outcomes. last_activity_at shows how fresh the data is.",
		true, false), func(ctx context.Context, req *mcp.CallToolRequest, in serviceIn) (*mcp.CallToolResult, any, error) {
		if !uuidPattern.MatchString(in.ServiceID) {
			return fail(msgBadServiceID)
		}
		return t.call(ctx, req, "get_live_traffic", scopeServices, http.MethodGet, "/v1/services/"+in.ServiceID+"/live-metrics", nil, nil)
	})

	mcp.AddTool(s, tool("get_usage_history", "Get usage history",
		"Returns usage history for one service, or for the whole account when service_id is empty: connection success, traffic and connection errors over the last 24h, 7d or 30d. In series, bytes_out is downloaded and bytes_in is uploaded. Can lag recent traffic.",
		true, false), func(ctx context.Context, req *mcp.CallToolRequest, in usageHistoryIn) (*mcp.CallToolResult, any, error) {
		path := "/v1/analytics"
		if in.ServiceID != "" {
			if !uuidPattern.MatchString(in.ServiceID) {
				return fail(msgBadServiceID)
			}
			path = "/v1/services/" + in.ServiceID + "/analytics"
		}
		return t.call(ctx, req, "get_usage_history", scopeServices, http.MethodGet, path, query("range", in.Range), nil)
	})

	mcp.AddTool(s, tool("list_invoices", "List invoices",
		"Lists invoices with status (open, paid or cancelled), kind, amount in cents and the service each belongs to. Paginated: pass the cursor from the previous response to get the next page.",
		true, false), func(ctx context.Context, req *mcp.CallToolRequest, in listInvoicesIn) (*mcp.CallToolResult, any, error) {
		if in.ServiceID != "" && !uuidPattern.MatchString(in.ServiceID) {
			return fail(msgBadServiceID)
		}
		q := query("service_id", in.ServiceID, "status", in.Status, "kind", in.Kind, "cursor", in.Cursor, "limit", num(in.Limit))
		return t.call(ctx, req, "list_invoices", scopeBilling, http.MethodGet, "/v1/invoices", q, nil)
	})

	mcp.AddTool(s, tool("get_invoice", "Get an invoice",
		"Returns one invoice: status, amounts in cents, payment attempts and its service. Does not start a payment.",
		true, false), func(ctx context.Context, req *mcp.CallToolRequest, in invoiceIn) (*mcp.CallToolResult, any, error) {
		if !uuidPattern.MatchString(in.InvoiceID) {
			return fail(msgBadInvoiceID)
		}
		return t.call(ctx, req, "get_invoice", scopeBilling, http.MethodGet, "/v1/invoices/"+in.InvoiceID, nil, nil)
	})

	mcp.AddTool(s, tool("generate_endpoints", "Generate endpoints",
		"Builds proxy connection lines for a service with the chosen protocol, format, location targeting and session type: one line, or up to 1,000 sticky sessions with session sticky. Does not change the service. The output contains the proxy username and password, which let anyone who has them use the service.",
		true, false), func(ctx context.Context, req *mcp.CallToolRequest, in endpointsIn) (*mcp.CallToolResult, any, error) {
		if !uuidPattern.MatchString(in.ServiceID) {
			return fail(msgBadServiceID)
		}
		body := targeting(in.Protocol, in.Country, in.City, in.Region, in.ASN, in.Session, in.Lifetime)
		if in.Format != "" {
			body["format"] = in.Format
		}
		if in.Count > 0 {
			body["count"] = in.Count
		}
		return t.call(ctx, req, "generate_endpoints", scopeProxy, http.MethodPost, "/v1/services/"+in.ServiceID+"/endpoints", nil, body)
	})

	mcp.AddTool(s, tool("check_connection", "Check connection",
		"Sends one request through the service from TrueProxies and reports the outcome and exit IP, to tell a proxy problem apart from a website refusing you. It counts as use of the service: it uses a little traffic and starts an unused free trial. Does not measure speed. Limited to a few checks a minute.",
		false, true), func(ctx context.Context, req *mcp.CallToolRequest, in checkIn) (*mcp.CallToolResult, any, error) {
		if !uuidPattern.MatchString(in.ServiceID) {
			return fail(msgBadServiceID)
		}
		body := targeting(in.Protocol, in.Country, in.City, in.Region, in.ASN, in.Session, in.Lifetime)
		return t.call(ctx, req, "check_connection", scopeProxy, http.MethodPost, "/v1/services/"+in.ServiceID+"/check", nil, body)
	})

	mcp.AddTool(s, tool("list_trusted_ips", "List trusted IPs",
		"Lists the trusted IPs of a service: addresses allowed to connect without the proxy password.",
		true, false), func(ctx context.Context, req *mcp.CallToolRequest, in serviceIn) (*mcp.CallToolResult, any, error) {
		if !uuidPattern.MatchString(in.ServiceID) {
			return fail(msgBadServiceID)
		}
		return t.call(ctx, req, "list_trusted_ips", scopeProxy, http.MethodGet, "/v1/services/"+in.ServiceID+"/whitelist", nil, nil)
	})
}

// listPlans filters the cached public catalog. It needs no API key.
func (t *tools) listPlans(ctx context.Context, _ *mcp.CallToolRequest, in plansIn) (*mcp.CallToolResult, any, error) {
	start := time.Now()
	data, rid, err := t.api.publicCatalog(ctx)
	logCall("list_plans", err, rid, start)
	if err != nil {
		return fail(toolError("list_plans", "", err))
	}
	var cat struct {
		Offers         []map[string]any `json:"offers"`
		Cities         json.RawMessage  `json:"cities"`
		TrialAvailable bool             `json:"trial_available"`
	}
	if err := json.Unmarshal(data, &cat); err != nil {
		return fail("The catalog could not be read: " + err.Error())
	}
	offers := []map[string]any{}
	for _, o := range cat.Offers {
		if o["enabled"] != true || o["product"] == "trial" {
			continue
		}
		if in.Product != "" && o["product"] != in.Product {
			continue
		}
		if in.Term != "" && o["term"] != in.Term {
			continue
		}
		delete(o, "floor_price_cents")
		offers = append(offers, o)
	}
	out, err := json.Marshal(map[string]any{"offers": offers, "cities": cat.Cities, "free_trial_available": cat.TrialAvailable})
	if err != nil {
		return fail("The catalog could not be read: " + err.Error())
	}
	return text(out), nil, nil
}

// call runs one API request for a tool that needs an API key and returns the
// response body as the tool result.
func (t *tools) call(ctx context.Context, req *mcp.CallToolRequest, name, scope, method, path string, q url.Values, body any) (*mcp.CallToolResult, any, error) {
	auth := t.auth(req)
	if auth == "" {
		return fail(msgNoKey)
	}
	start := time.Now()
	data, rid, err := t.api.do(ctx, method, path, q, body, auth)
	logCall(name, err, rid, start)
	if err != nil {
		return fail(toolError(name, scope, err))
	}
	return text(data), nil, nil
}

// toolError turns an API failure into one plain sentence. It never includes
// the request's Authorization header or body.
func toolError(name, scope string, err error) string {
	var ae *apiError
	if !errors.As(err, &ae) {
		return "The TrueProxies API could not be reached: " + err.Error()
	}
	ref := ""
	if ae.RequestID != "" {
		ref = " (request ID " + ae.RequestID + ")"
	}
	switch {
	case ae.Status == http.StatusUnauthorized:
		return "The API key is invalid, expired or revoked." + ref
	case ae.Status == http.StatusForbidden && scope != "":
		return fmt.Sprintf("%s. This tool needs an API key with the %s scope.%s", strings.TrimRight(ae.Message, "."), scope, ref)
	case ae.Status == http.StatusNotFound:
		return "No service or invoice with that ID on this account." + ref
	case ae.Status == http.StatusTooManyRequests:
		wait := ae.RetryAfter
		if _, err := strconv.Atoi(wait); err != nil {
			wait = "60"
		}
		return fmt.Sprintf("Rate limited. Try again in %s seconds.%s", wait, ref)
	case ae.Status == http.StatusServiceUnavailable && name == "get_live_traffic":
		return "Live traffic is unavailable right now." + ref
	default:
		return fmt.Sprintf("%s (HTTP %d)%s", ae.Message, ae.Status, ref)
	}
}

// logCall writes one line per tool call: never arguments, results or headers.
func logCall(name string, err error, rid string, start time.Time) {
	status := http.StatusOK
	var ae *apiError
	switch {
	case errors.As(err, &ae):
		status = ae.Status
	case err != nil:
		status = 0
	}
	log.Printf("tool=%s status=%d request_id=%s duration=%s", name, status, rid, time.Since(start).Round(time.Millisecond))
}

// providerFields are supplier balances in the usage response. They are not the
// customer's traffic, so they stay out of the tool result.
var providerFields = []string{"gb_allocated", "gb_remaining", "gb_used", "provider_observed_at", "provider_usage_informational", "reconciliation"}

// without removes top-level fields from a successful JSON tool result.
func without(res *mcp.CallToolResult, fields ...string) *mcp.CallToolResult {
	if res.IsError || len(res.Content) == 0 {
		return res
	}
	tc, ok := res.Content[0].(*mcp.TextContent)
	if !ok {
		return res
	}
	var m map[string]json.RawMessage
	if json.Unmarshal([]byte(tc.Text), &m) != nil {
		return res
	}
	for _, f := range fields {
		delete(m, f)
	}
	b, err := json.Marshal(m)
	if err != nil {
		return res
	}
	return text(b)
}

func text(b []byte) *mcp.CallToolResult {
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: string(b)}}}
}

func fail(msg string) (*mcp.CallToolResult, any, error) {
	return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: msg}}}, nil, nil
}

// query builds query parameters from name, value pairs, skipping empty values.
func query(kv ...string) url.Values {
	q := url.Values{}
	for i := 0; i+1 < len(kv); i += 2 {
		if kv[i+1] != "" {
			q.Set(kv[i], kv[i+1])
		}
	}
	return q
}

func num(n int) string {
	if n <= 0 {
		return ""
	}
	return strconv.Itoa(n)
}

// targeting builds the shared endpoint and check request body.
func targeting(protocol, country, city, region, asn, session string, lifetime int) map[string]any {
	b := map[string]any{}
	for k, v := range map[string]string{"protocol": protocol, "country": country, "city": city, "region": region, "asn": asn, "session": session} {
		if v != "" {
			b[k] = v
		}
	}
	if lifetime > 0 {
		b["lifetime"] = lifetime
	}
	return b
}
