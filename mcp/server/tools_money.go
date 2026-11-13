package server

import (
	"context"
	"fmt"
	"math/big"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	api "github.com/survivalservers/studio-go"
)

type poolsIn struct {
	App string `json:"app" jsonschema:"the app's slug or ID"`
}

type setPoolIn struct {
	App      string  `json:"app" jsonschema:"the app's slug or ID"`
	Region   string  `json:"region" jsonschema:"the region the ready servers wait in (from list_regions)"`
	Version  string  `json:"version,omitempty" jsonschema:"the version they run (or branch)"`
	Branch   string  `json:"branch,omitempty" jsonschema:"follow this branch's version (or version)"`
	CPU      float64 `json:"cpu" jsonschema:"vCPUs per ready server (the size a start must ask for to get one)"`
	MemoryGB float64 `json:"memory_gb" jsonschema:"memory in GB per ready server"`
	Mode     string  `json:"mode" jsonschema:"fixed: always min ready; auto: grows with recent starts, from min up to max"`
	Min      int     `json:"min" jsonschema:"ready servers kept waiting"`
	Max      *int    `json:"max,omitempty" jsonschema:"auto: the most it grows to"`
}

type removePoolIn struct {
	App    string `json:"app" jsonschema:"the app's slug or ID"`
	Region string `json:"region" jsonschema:"the pool's region"`
}

type usageIn struct {
	From        string   `json:"from,omitempty" jsonschema:"start date or time (RFC 3339 or YYYY-MM-DD); default the start of this month"`
	To          string   `json:"to,omitempty" jsonschema:"end date or time; default now"`
	GroupBy     []string `json:"group_by,omitempty" jsonschema:"split the rows by app, region, rate_class or version"`
	Granularity string   `json:"granularity,omitempty" jsonschema:"hour, day (default) or month"`
}

type estimateIn struct {
	Kind     string  `json:"kind,omitempty" jsonschema:"on_demand (default: game servers started per match), persistent_server, reserved, or warm (ready servers waiting in a pool)"`
	CPU      float64 `json:"cpu,omitempty" jsonschema:"vCPUs per game server"`
	MemoryGB float64 `json:"memory_gb,omitempty" jsonschema:"memory in GB per game server (above 2 GB per CPU bills at the 4 GB rate)"`
	Hours    float64 `json:"hours,omitempty" jsonschema:"hours each one runs"`
	Count    int     `json:"count,omitempty" jsonschema:"how many game servers (default 1)"`
}

func (s *srv) addMoneyTools(server *mcp.Server) {
	tool(s, server, "list_pools", "List pools", "An app's pools of ready servers: per region, how many game servers are kept started and waiting for a match, and how many are ready now.", reads, nil,
		func(ctx context.Context, _ *mcp.CallToolRequest, in poolsIn) (*mcp.CallToolResult, error) {
			if err := s.allowedApp(ctx, in.App); err != nil {
				return nil, err
			}
			page, err := s.client.Get(ctx, "/v0/apps/"+api.PathEscape(in.App)+"/pools", url.Values{"limit": {"500"}})
			if err != nil {
				return nil, err
			}
			rows := list(page["data"])
			return textResult(fmt.Sprintf("%d pools for %s.", len(rows), in.App), rows), nil
		})

	tool(s, server, "set_pool", "Set a pool", "Keeps ready servers of a version started and waiting in a region, so a match start takes one at once instead of booting it. Ready servers are billed while they wait; that price is not final.", changes,
		map[string][]string{"mode": {"fixed", "auto"}},
		func(ctx context.Context, _ *mcp.CallToolRequest, in setPoolIn) (*mcp.CallToolResult, error) {
			if err := s.allowedApp(ctx, in.App); err != nil {
				return nil, err
			}
			if (in.Version == "") == (in.Branch == "") {
				return nil, fmt.Errorf("give either version or branch")
			}
			if in.Min < 0 || (in.Max != nil && *in.Max < in.Min) {
				return nil, fmt.Errorf("min must be 0 or more and max at least min")
			}
			body := map[string]any{"size": map[string]any{"cpu": in.CPU, "memory_gb": in.MemoryGB}, "mode": in.Mode, "min": in.Min}
			setIf(body, "version", in.Version)
			setIf(body, "branch", in.Branch)
			if in.Max != nil {
				body["max"] = *in.Max
			}
			var pool map[string]any
			if err := s.client.JSON(ctx, api.Call{Method: http.MethodPut, Path: "/v0/apps/" + api.PathEscape(in.App) + "/pools/" + api.PathEscape(in.Region), Body: body}, &pool); err != nil {
				return nil, err
			}
			st := obj(pool["status"])
			return textResult(fmt.Sprintf("Pool for %s in %s set: target %v, %v ready now.", in.App, in.Region, st["target"], st["ready"]), pool), nil
		})

	tool(s, server, "remove_pool", "Remove a pool", "Removes an app's pool in a region; its waiting ready servers are stopped. Matches there then start fresh.", removes, nil,
		func(ctx context.Context, _ *mcp.CallToolRequest, in removePoolIn) (*mcp.CallToolResult, error) {
			if err := s.allowedApp(ctx, in.App); err != nil {
				return nil, err
			}
			if _, err := s.client.Do(ctx, api.Call{Method: http.MethodDelete, Path: "/v0/apps/" + api.PathEscape(in.App) + "/pools/" + api.PathEscape(in.Region)}); err != nil {
				return nil, err
			}
			return plainResult(fmt.Sprintf("Pool for %s in %s removed.", in.App, in.Region)), nil
		})

	tool(s, server, "get_usage", "Usage and cost", "vCPU-hours used and what they cost, for this month by default: totals (with free vCPU-hours used, credits and the month's projection) and rows by day or split by app, region, rate class or version.", reads,
		map[string][]string{"group_by": {"app", "region", "rate_class", "version"}, "granularity": {"hour", "day", "month"}},
		func(ctx context.Context, _ *mcp.CallToolRequest, in usageIn) (*mcp.CallToolResult, error) {
			q := url.Values{}
			from, to := in.From, in.To
			if from == "" {
				now := time.Now().UTC()
				from = time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC).Format(time.RFC3339)
			}
			if to == "" {
				to = time.Now().UTC().Format(time.RFC3339)
			}
			q.Set("from", dayStart(from))
			q.Set("to", dayStart(to))
			groupBy := in.GroupBy
			if s.limited() && !slices.Contains(groupBy, "app") {
				groupBy = append([]string{"app"}, groupBy...)
			}
			if len(groupBy) > 0 {
				q.Set("group_by", strings.Join(groupBy, ","))
			}
			q.Set("granularity", firstOf(in.Granularity, "day"))
			u, err := s.client.Get(ctx, "/v0/usage", q)
			if err != nil {
				return nil, err
			}
			if s.limited() {
				return s.appUsage(ctx, q.Get("from"), u)
			}
			t := obj(u["totals"])
			return textResult(fmt.Sprintf("From %s: %s vCPU-hours, $%s (free vCPU-hours used %s, credits $%s; this month is heading for $%s).",
				q.Get("from"), str(t["cpu_hours"]), str(t["amount_usd"]), str(t["free_cpu_hours_used"]), str(t["credits_usd"]), str(t["projected_month_usd"])), u), nil
		})

	tool(s, server, "get_prices", "Prices", "The studio's prices per vCPU-hour (on demand by memory, reserved, persistent servers, paused, ready servers), the free vCPU-hours each month, and until when these prices are locked. Bandwidth is included.", reads, nil,
		func(ctx context.Context, _ *mcp.CallToolRequest, _ noInput) (*mcp.CallToolResult, error) {
			p, err := s.client.Get(ctx, "/v0/prices", nil)
			if err != nil {
				return nil, err
			}
			return textResult(fmt.Sprintf("Prices in %s, locked until %s.", str(p["currency"]), str(p["locked_until"])), p), nil
		})

	tool(s, server, "estimate_cost", "Estimate a cost", "Works out what game servers would cost at the studio's own prices: vCPUs times hours times count at the rate for the kind and memory. Use it instead of doing the sum yourself.", reads,
		map[string][]string{"kind": {"on_demand", "persistent_server", "reserved", "warm"}},
		func(ctx context.Context, _ *mcp.CallToolRequest, in estimateIn) (*mcp.CallToolResult, error) {
			p, err := s.client.Get(ctx, "/v0/prices", nil)
			if err != nil {
				return nil, err
			}
			if in.CPU <= 0 || in.Hours <= 0 {
				return nil, fmt.Errorf("give cpu and hours (and memory_gb for on_demand)")
			}
			count := max(in.Count, 1)
			rates := obj(p["rates"])
			kind := firstOf(in.Kind, "on_demand")
			var rateName string
			warmGuess := false
			switch kind {
			case "warm":
				if _, ok := rates["warm_cpu_hour"]; ok {
					rateName = "warm_cpu_hour"
					break
				}
				warmGuess = true
				fallthrough // no ready server price published yet: estimated at the on-demand rate
			case "on_demand":
				if in.MemoryGB <= 0 {
					return nil, fmt.Errorf("on_demand needs memory_gb: up to 2 GB per CPU bills at one rate, above that at another")
				}
				rateName = "on_demand_cpu_hour_2gb"
				if in.MemoryGB > 2*in.CPU {
					rateName = "on_demand_cpu_hour_4gb"
				}
			case "persistent_server":
				rateName = "persistent_server_cpu_hour"
			case "reserved":
				rateName = "reserved_cpu_hour"
			default:
				return nil, fmt.Errorf("kind must be on_demand, persistent_server, reserved or warm")
			}
			rate, ok := new(big.Rat).SetString(str(rates[rateName]))
			if !ok {
				return nil, fmt.Errorf("the price list has no %s rate", rateName)
			}
			cpuHours := new(big.Rat).Mul(ratOf(in.CPU), ratOf(in.Hours))
			cpuHours.Mul(cpuHours, big.NewRat(int64(count), 1))
			amount := new(big.Rat).Mul(cpuHours, rate)
			text := fmt.Sprintf("%d x %g vCPU for %g hours = %s vCPU-hours at $%s per vCPU-hour (%s) = $%s.",
				count, in.CPU, in.Hours, trimZeros(cpuHours.FloatString(2)), str(rates[rateName]), rateName, amount.FloatString(2))
			if rateName == "reserved_cpu_hour" {
				// A reservation is billed for every hour it holds, used or not, and free vCPU-hours never apply to it
				// (as the site's calculator counts it).
				text += " A reservation is billed for every hour it holds, used or not; free vCPU-hours do not apply to it."
			} else if free := str(p["free_cpu_hours_per_month"]); free != "" && free != "0" {
				text += fmt.Sprintf(" The first %s vCPU-hours each month are free; get_account shows how many are left this month.", free)
			}
			if warmGuess {
				text += " The ready server price is not final; this uses the on-demand rate."
			}
			text += " Bandwidth is included."
			return plainResult(text), nil
		})
}

// appUsage is a usage report cut to the apps GS_APPS allows: their rows only, summed. The studio's totals, its
// free vCPU-hours and its rows by date cover every app, so they are left out.
func (s *srv) appUsage(ctx context.Context, from string, u map[string]any) (*mcp.CallToolResult, error) {
	rows, err := s.filterAllowed(ctx, list(u["rows"]), "app")
	if err != nil {
		return nil, err
	}
	cpu, amount := new(big.Rat), new(big.Rat)
	for _, r := range rows {
		if v, ok := new(big.Rat).SetString(str(r["cpu_hours"])); ok {
			cpu.Add(cpu, v)
		}
		if v, ok := new(big.Rat).SetString(str(r["amount_usd"])); ok {
			amount.Add(amount, v)
		}
	}
	text := fmt.Sprintf("From %s, for %s only: %s vCPU-hours, $%s before free vCPU-hours and credits. This server is limited to those apps (GS_APPS), so the studio's totals are left out.",
		from, strings.Join(s.cfg.Apps, ", "), trimZeros(cpu.FloatString(4)), amount.FloatString(2))
	return textResult(text, map[string]any{"currency": u["currency"], "rows": rows}), nil
}

func ratOf(f float64) *big.Rat {
	r, _ := new(big.Rat).SetString(fmt.Sprintf("%g", f))
	if r == nil {
		return new(big.Rat)
	}
	return r
}

func trimZeros(s string) string {
	if strings.Contains(s, ".") {
		s = strings.TrimRight(strings.TrimRight(s, "0"), ".")
	}
	return s
}

// dayStart turns a bare date into the start of that day in UTC; anything else is passed on.
func dayStart(v string) string {
	if t, err := time.Parse("2006-01-02", v); err == nil {
		return t.UTC().Format(time.RFC3339)
	}
	return v
}
