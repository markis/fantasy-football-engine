package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"ff-engine/internal/health"
	"ff-engine/internal/query"
)

var (
	errPipelineTriggerNotEnabled   = errors.New("pipeline trigger not enabled")
	errInvalidRankingsSourceMarket = errors.New("invalid source/market combination")
)

const (
	schemaTypeObject  = "object"
	schemaProperties  = "properties"
	schemaType        = "type"
	schemaTypeString  = "string"
	schemaTypeInteger = "integer"
	schemaTypeBoolean = "boolean"
	schemaTypeArray   = "array"
	schemaDescription = "description"
	schemaDefault     = "default"
	schemaRequired    = "required"
	schemaItems       = "items"
	contentTypeJSON   = "application/json"
	statusOK          = "ok"
	toolStatus        = "status"
	toolService       = "service"
	toolsKey          = "tools"
	textType          = "text"
	jsonrpcVersion    = "2.0"
	isErrorKey        = "isError"
	contentKey        = "content"
	paramPosition     = "position"
	paramSuperflex    = "superflex"
	paramLeagueID     = "league_id"
	paramUserID       = "user_id"
	paramWeek         = "week"
	paramStep         = "step"
	paramQuery        = "query"
	paramPlayerID     = "player_id"
	paramLimit        = "limit"
	paramDays         = "days"
	paramRelevantOnly = "relevant_only"
)

// newsSearchFunc is the signature shared by SearchNews.
type newsSearchFunc func(ctx context.Context, query string, limit int, days *int, relevantOnly bool) ([]map[string]any, error)

// Server is the MCP server that exposes tools over Streamable HTTP.
// It implements a minimal MCP-compatible JSON-RPC handler that supports
// tools/list and tools/call.
type Server struct {
	query   *query.Service
	addr    string
	tools   map[string]Tool
	mu      sync.RWMutex
	trigger func(ctx context.Context, step string) error
	checker *health.Checker
}

// Tool is an MCP tool definition.
type Tool struct {
	Name        string
	Description string
	InputSchema map[string]any
	Handler     func(ctx context.Context, args map[string]any) (any, error)
}

// New creates a new MCP server.
func New(addr string, queryService *query.Service) *Server {
	s := &Server{
		query: queryService,
		addr:  addr,
		tools: make(map[string]Tool),
	}
	s.registerTools()
	return s
}

// SetTrigger sets the pipeline trigger function (for trigger_pipeline tool).
func (s *Server) SetTrigger(fn func(ctx context.Context, step string) error) {
	s.trigger = fn
}

// SetHealthChecker sets the readiness checker exposed at GET /readyz. When
// unset, /readyz falls back to the liveness response (no dependency probe).
func (s *Server) SetHealthChecker(c *health.Checker) {
	s.checker = c
}

// registerTools registers all MCP tools.
func (s *Server) registerTools() {
	// News & stories
	s.registerNewsSearchTool("search_news",
		"Semantic search over fantasy football news items using chunked embeddings, returning unique news items.",
		s.query.SearchNews)

	s.registerTool(Tool{
		Name:        "get_stories",
		Description: "Get top story clusters from a time window.",
		InputSchema: map[string]any{
			schemaType: schemaTypeObject,
			schemaProperties: map[string]any{
				"hours":    map[string]any{schemaType: schemaTypeInteger, schemaDefault: 24},
				paramLimit: map[string]any{schemaType: schemaTypeInteger, schemaDefault: 5},
			},
		},
		Handler: func(ctx context.Context, args map[string]any) (any, error) {
			hours := getInt(args, "hours", 24)
			limit := getInt(args, paramLimit, 5)
			return s.query.GetStories(ctx, hours, limit)
		},
	})

	s.registerTool(Tool{
		Name:        "search_facts",
		Description: "Semantic search over extracted fantasy football facts.",
		InputSchema: map[string]any{
			schemaType: schemaTypeObject,
			schemaProperties: map[string]any{
				paramQuery: map[string]any{schemaType: schemaTypeString},
				paramLimit: map[string]any{schemaType: schemaTypeInteger, schemaDefault: 10},
			},
			schemaRequired: []string{paramQuery},
		},
		Handler: func(ctx context.Context, args map[string]any) (any, error) {
			q := getStr(args, paramQuery)
			limit := getInt(args, paramLimit, 10)
			return s.query.SearchFacts(ctx, q, limit)
		},
	})

	s.registerTool(Tool{
		Name:        "get_recent_news",
		Description: "Get the latest N news items, optionally filtered to fantasy-relevant only.",
		InputSchema: map[string]any{
			schemaType: schemaTypeObject,
			schemaProperties: map[string]any{
				paramLimit:      map[string]any{schemaType: schemaTypeInteger, schemaDefault: 10},
				"relevant_only": map[string]any{schemaType: schemaTypeBoolean, schemaDefault: false},
			},
		},
		Handler: func(ctx context.Context, args map[string]any) (any, error) {
			limit := getInt(args, "limit", 10)
			relevant := getBool(args, "relevant_only")
			return s.query.GetRecentNews(ctx, limit, relevant)
		},
	})

	// Players & valuations
	s.registerTool(Tool{
		Name:        "search_players",
		Description: "Search NFL players by name, with optional position filter.",
		InputSchema: map[string]any{
			schemaType: schemaTypeObject,
			schemaProperties: map[string]any{
				paramQuery:    map[string]any{schemaType: schemaTypeString},
				paramPosition: map[string]any{schemaType: schemaTypeString},
				paramLimit:    map[string]any{schemaType: schemaTypeInteger, schemaDefault: 25},
			},
			schemaRequired: []string{paramQuery},
		},
		Handler: func(ctx context.Context, args map[string]any) (any, error) {
			q := getStr(args, "query")
			limit := getInt(args, "limit", 25)
			var pos *string
			if p := getStr(args, paramPosition); p != "" {
				pos = &p
			}
			return s.query.SearchPlayers(ctx, q, pos, limit)
		},
	})

	s.registerTool(Tool{
		Name:        "get_player",
		Description: "Get a full player profile by Sleeper player ID.",
		InputSchema: map[string]any{
			schemaType: schemaTypeObject,
			schemaProperties: map[string]any{
				"player_id": map[string]any{schemaType: schemaTypeString},
			},
			"required": []string{"player_id"},
		},
		Handler: func(ctx context.Context, args map[string]any) (any, error) {
			pid := getStr(args, "player_id")
			return s.query.GetPlayer(ctx, pid)
		},
	})

	s.registerTool(Tool{
		Name: "get_rankings",
		Description: "Get dynasty trade value rankings. Sources: " +
			"FantasyCalc (markets 1/2/3), Dynasty Daddy (market 14), " +
			"KeepTradeCut (market 0), FantasyPros ECR (market 1). " +
			"If only source is given, market is derived; if only market is given, " +
			"source is derived. With no args, defaults to Dynasty Daddy market 14.",
		InputSchema: map[string]any{
			schemaType: schemaTypeObject,
			schemaProperties: map[string]any{
				"position":     map[string]any{schemaType: schemaTypeString},
				"limit":        map[string]any{schemaType: schemaTypeInteger, schemaDefault: 15},
				"source":       map[string]any{schemaType: schemaTypeString},
				"market":       map[string]any{schemaType: schemaTypeInteger},
				paramSuperflex: map[string]any{schemaType: schemaTypeBoolean, schemaDefault: false},
			},
		},
		Handler: func(ctx context.Context, args map[string]any) (any, error) {
			limit := getInt(args, "limit", 15)
			superflex := getBool(args, paramSuperflex)
			var pos *string
			if p := getStr(args, paramPosition); p != "" {
				pos = &p
			}
			source, market, ok := resolveRankingsSourceMarket(args)
			if !ok {
				return nil, fmt.Errorf("%w: source=%q market=%d", errInvalidRankingsSourceMarket, getStr(args, "source"), getInt(args, "market", -1))
			}
			return s.query.GetRankings(ctx, pos, limit, source, market, superflex)
		},
	})

	s.registerTool(Tool{
		Name:        "get_trending_players",
		Description: "Get trending players (adds/drops) from Sleeper.",
		InputSchema: map[string]any{
			schemaType: schemaTypeObject,
			schemaProperties: map[string]any{
				"trend_type": map[string]any{schemaType: schemaTypeString, schemaDefault: "add"},
				paramLimit:   map[string]any{schemaType: schemaTypeInteger, schemaDefault: 25},
			},
		},
		Handler: func(ctx context.Context, args map[string]any) (any, error) {
			tt := getStr(args, "trend_type")
			limit := getInt(args, "limit", 25)
			return s.query.GetTrendingPlayers(ctx, tt, limit)
		},
	})

	s.registerTool(Tool{
		Name:        "get_free_agents",
		Description: "Get top ranked free agents (unowned players) in a league.",
		InputSchema: map[string]any{
			schemaType: schemaTypeObject,
			schemaProperties: map[string]any{
				paramLeagueID:  map[string]any{schemaType: schemaTypeString},
				paramPosition:  map[string]any{schemaType: schemaTypeString},
				paramLimit:     map[string]any{schemaType: schemaTypeInteger, schemaDefault: 15},
				paramSuperflex: map[string]any{schemaType: schemaTypeBoolean, schemaDefault: false},
			},
			schemaRequired: []string{paramLeagueID},
		},
		Handler: func(ctx context.Context, args map[string]any) (any, error) {
			leagueID := getStr(args, paramLeagueID)
			limit := getInt(args, "limit", 15)
			superflex := getBool(args, paramSuperflex)
			var pos *string
			if p := getStr(args, paramPosition); p != "" {
				pos = &p
			}
			return s.query.GetFreeAgents(ctx, leagueID, pos, limit, superflex)
		},
	})

	// Leagues
	s.registerTool(Tool{
		Name:        "get_nfl_state",
		Description: "Get the current NFL state (week, season, season type).",
		InputSchema: map[string]any{schemaType: schemaTypeObject},
		Handler: func(ctx context.Context, _ map[string]any) (any, error) {
			return s.query.GetNFLState(ctx)
		},
	})

	// Team & assessment
	s.registerTool(Tool{
		Name:        "evaluate_roster",
		Description: "Get structured roster data with trade values for a user in a league. Returns data only — the agent does the reasoning.",
		InputSchema: map[string]any{
			schemaType: schemaTypeObject,
			schemaProperties: map[string]any{
				paramLeagueID:  map[string]any{schemaType: schemaTypeString},
				paramUserID:    map[string]any{schemaType: schemaTypeString, schemaDefault: "558115100726579200"},
				paramSuperflex: map[string]any{schemaType: schemaTypeBoolean, schemaDefault: false},
			},
			schemaRequired: []string{paramLeagueID},
		},
		Handler: func(ctx context.Context, args map[string]any) (any, error) {
			leagueID := getStr(args, paramLeagueID)
			userID := getStr(args, paramUserID)
			if userID == "" {
				userID = "558115100726579200"
			}
			superflex := getBool(args, paramSuperflex)
			return s.query.EvaluateRoster(ctx, leagueID, userID, superflex)
		},
	})

	s.registerTool(Tool{
		Name: "evaluate_trade",
		Description: "Evaluate a dynasty trade proposal. Returns structured data: both sides' players " +
			"with trade values, totals, delta, and a recommendation. The agent does the reasoning.",
		InputSchema: map[string]any{
			schemaType: schemaTypeObject,
			schemaProperties: map[string]any{
				"give":         map[string]any{schemaType: schemaTypeArray, schemaItems: map[string]any{schemaType: schemaTypeString}},
				"get":          map[string]any{schemaType: schemaTypeArray, schemaItems: map[string]any{schemaType: schemaTypeString}},
				paramLeagueID:  map[string]any{schemaType: schemaTypeString},
				paramSuperflex: map[string]any{schemaType: schemaTypeBoolean, schemaDefault: false},
			},
			schemaRequired: []string{"give", "get"},
		},
		Handler: func(ctx context.Context, args map[string]any) (any, error) {
			give := toStrSlice(args["give"])
			get := toStrSlice(args["get"])
			leagueID := getStr(args, paramLeagueID)
			superflex := getBool(args, paramSuperflex)
			return s.query.EvaluateTrade(ctx, give, get, leagueID, superflex)
		},
	})

	// Sleeper league / user / draft passthrough tools (absorbed from the old stdio MCP)
	s.registerTool(Tool{
		Name:        "get_user_info",
		Description: "Fetch Sleeper user info by username or user ID.",
		InputSchema: map[string]any{
			schemaType: schemaTypeObject,
			schemaProperties: map[string]any{
				"username_or_user_id": map[string]any{schemaType: schemaTypeString},
			},
			schemaRequired: []any{"username_or_user_id"},
		},
		Handler: func(ctx context.Context, args map[string]any) (any, error) {
			return s.query.GetUserInfo(ctx, getStr(args, "username_or_user_id"))
		},
	})
	s.registerTool(Tool{
		Name:        "get_user_leagues",
		Description: "Fetch a Sleeper user's leagues for a season (sport nfl).",
		InputSchema: map[string]any{
			schemaType: schemaTypeObject,
			schemaProperties: map[string]any{
				paramUserID: map[string]any{schemaType: schemaTypeString},
				"season":    map[string]any{schemaType: schemaTypeString, schemaDefault: "2026"},
			},
			schemaRequired: []any{paramUserID},
		},
		Handler: func(ctx context.Context, args map[string]any) (any, error) {
			season := getStr(args, "season")
			if season == "" {
				season = "2026"
			}
			return s.query.GetUserLeagues(ctx, getStr(args, paramUserID), season)
		},
	})
	s.registerTool(Tool{
		Name:        "get_league_info",
		Description: "Fetch Sleeper league info (settings, roster positions, scoring).",
		InputSchema: map[string]any{
			schemaType: schemaTypeObject,
			schemaProperties: map[string]any{
				paramLeagueID: map[string]any{schemaType: schemaTypeString},
			},
			schemaRequired: []any{paramLeagueID},
		},
		Handler: func(ctx context.Context, args map[string]any) (any, error) {
			return s.query.GetLeagueInfo(ctx, getStr(args, paramLeagueID))
		},
	})
	s.registerTool(Tool{
		Name:        "get_league_rosters",
		Description: "Fetch all rosters for a Sleeper league.",
		InputSchema: map[string]any{
			schemaType: schemaTypeObject,
			schemaProperties: map[string]any{
				paramLeagueID: map[string]any{schemaType: schemaTypeString},
			},
			schemaRequired: []any{paramLeagueID},
		},
		Handler: func(ctx context.Context, args map[string]any) (any, error) {
			return s.query.GetLeagueRosters(ctx, getStr(args, paramLeagueID))
		},
	})
	s.registerTool(Tool{
		Name:        "get_league_users",
		Description: "Fetch all managers (users) for a Sleeper league.",
		InputSchema: map[string]any{
			schemaType: schemaTypeObject,
			schemaProperties: map[string]any{
				paramLeagueID: map[string]any{schemaType: schemaTypeString},
			},
			schemaRequired: []any{paramLeagueID},
		},
		Handler: func(ctx context.Context, args map[string]any) (any, error) {
			return s.query.GetLeagueUsers(ctx, getStr(args, paramLeagueID))
		},
	})
	s.registerTool(Tool{
		Name:        "get_league_matchups",
		Description: "Fetch matchups for a Sleeper league in a given week.",
		InputSchema: map[string]any{
			schemaType: schemaTypeObject,
			schemaProperties: map[string]any{
				paramLeagueID: map[string]any{schemaType: schemaTypeString},
				paramWeek:     map[string]any{schemaType: schemaTypeInteger},
			},
			schemaRequired: []any{paramLeagueID, paramWeek},
		},
		Handler: func(ctx context.Context, args map[string]any) (any, error) {
			return s.query.GetLeagueMatchups(ctx, getStr(args, paramLeagueID), getInt(args, paramWeek, 0))
		},
	})
	s.registerTool(Tool{
		Name:        "get_league_transactions",
		Description: "Fetch transactions (trades/waivers) for a Sleeper league in a given week.",
		InputSchema: map[string]any{
			schemaType: schemaTypeObject,
			schemaProperties: map[string]any{
				paramLeagueID: map[string]any{schemaType: schemaTypeString},
				paramWeek:     map[string]any{schemaType: schemaTypeInteger},
			},
			schemaRequired: []any{paramLeagueID, paramWeek},
		},
		Handler: func(ctx context.Context, args map[string]any) (any, error) {
			return s.query.GetLeagueTransactions(ctx, getStr(args, paramLeagueID), getInt(args, paramWeek, 0))
		},
	})
	s.registerTool(Tool{
		Name:        "get_league_drafts",
		Description: "Fetch drafts for a Sleeper league.",
		InputSchema: map[string]any{
			schemaType: schemaTypeObject,
			schemaProperties: map[string]any{
				paramLeagueID: map[string]any{schemaType: schemaTypeString},
			},
			schemaRequired: []any{paramLeagueID},
		},
		Handler: func(ctx context.Context, args map[string]any) (any, error) {
			return s.query.GetLeagueDrafts(ctx, getStr(args, paramLeagueID))
		},
	})
	s.registerTool(Tool{
		Name:        "get_league_traded_picks",
		Description: "Fetch traded picks for a Sleeper league.",
		InputSchema: map[string]any{
			schemaType: schemaTypeObject,
			schemaProperties: map[string]any{
				paramLeagueID: map[string]any{schemaType: schemaTypeString},
			},
			schemaRequired: []any{paramLeagueID},
		},
		Handler: func(ctx context.Context, args map[string]any) (any, error) {
			return s.query.GetLeagueTradedPicks(ctx, getStr(args, paramLeagueID))
		},
	})

	// Leaguemate intelligence (cross-league profiling; absorbed from the old stdio MCP)
	s.registerTool(Tool{
		Name: "leaguemate_overlap",
		Description: "For a Sleeper player_id, show which leaguemates own that player across ALL their " +
			"leagues (yours + their others), with overlap counts. A leaguemate who owns a player in " +
			"many leagues values them above market. Resolve a name to player_id with search_players first.",
		InputSchema: map[string]any{
			schemaType: schemaTypeObject,
			schemaProperties: map[string]any{
				paramPlayerID:    map[string]any{schemaType: schemaTypeString},
				"include_markis": map[string]any{schemaType: schemaTypeBoolean, schemaDefault: false},
			},
			schemaRequired: []any{paramPlayerID},
		},
		Handler: func(ctx context.Context, args map[string]any) (any, error) {
			return s.query.LeaguemateOverlap(ctx, getStr(args, paramPlayerID), getBool(args, "include_markis"))
		},
	})
	s.registerTool(Tool{
		Name: "manager_profile",
		Description: "Build a cross-league dossier for a leaguemate by their Sleeper @username or " +
			"display_name: every league they manage (flagging yours), aggregate standings, and players " +
			"they hold in >=2 of their leagues. Use before any trade negotiation.",
		InputSchema: map[string]any{
			schemaType: schemaTypeObject,
			schemaProperties: map[string]any{
				"username": map[string]any{schemaType: schemaTypeString},
			},
			schemaRequired: []any{"username"},
		},
		Handler: func(ctx context.Context, args map[string]any) (any, error) {
			return s.query.ManagerProfile(ctx, getStr(args, "username"))
		},
	})
	s.registerTool(Tool{
		Name: "player_trade_value",
		Description: "Show what a player has ACTUALLY been traded for across all tracked leagues — the " +
			"true-market-price anchor. Returns recent completed trades with the full package (players by " +
			"name + picks). Resolve a name to player_id with search_players first.",
		InputSchema: map[string]any{
			schemaType: schemaTypeObject,
			schemaProperties: map[string]any{
				paramPlayerID: map[string]any{schemaType: schemaTypeString},
				"limit":       map[string]any{schemaType: schemaTypeInteger, schemaDefault: 12},
			},
			schemaRequired: []any{paramPlayerID},
		},
		Handler: func(ctx context.Context, args map[string]any) (any, error) {
			return s.query.PlayerTradeValue(ctx, getStr(args, paramPlayerID), getInt(args, "limit", 12))
		},
	})

	// Self-study
	s.registerTool(Tool{
		Name:        "get_study_material",
		Description: "Get a digest of recent stories + news for agent self-study. Returns markdown + structured data.",
		InputSchema: map[string]any{
			schemaType: schemaTypeObject,
			schemaProperties: map[string]any{
				"hours": map[string]any{"type": "integer", "default": 24},
			},
		},
		Handler: func(ctx context.Context, args map[string]any) (any, error) {
			hours := getInt(args, "hours", 24)
			return s.query.GetStudyMaterial(ctx, hours)
		},
	})

	// Pipeline control
	s.registerTool(Tool{
		Name:        "trigger_pipeline",
		Description: "Manually trigger a named pipeline step (e.g. fetch_rss, enrich, publish_daily). Requires pipeline trigger to be enabled.",
		InputSchema: map[string]any{
			schemaType: schemaTypeObject,
			schemaProperties: map[string]any{
				paramStep: map[string]any{schemaType: schemaTypeString, schemaDescription: "Pipeline step name"},
			},
			schemaRequired: []string{paramStep},
		},
		Handler: func(ctx context.Context, args map[string]any) (any, error) {
			if s.trigger == nil {
				return nil, errPipelineTriggerNotEnabled
			}
			step := getStr(args, paramStep)
			if err := s.trigger(ctx, step); err != nil {
				return nil, err
			}
			return map[string]any{toolStatus: statusOK, paramStep: step}, nil
		},
	})
}

func (s *Server) registerNewsSearchTool(name, description string, search newsSearchFunc) {
	s.registerTool(Tool{
		Name:        name,
		Description: description,
		InputSchema: map[string]any{
			schemaType: schemaTypeObject,
			schemaProperties: map[string]any{
				paramQuery:        map[string]any{schemaType: schemaTypeString, schemaDescription: "Search query"},
				paramLimit:        map[string]any{schemaType: schemaTypeInteger, schemaDefault: 10},
				paramDays:         map[string]any{schemaType: schemaTypeInteger, schemaDescription: "Only items from last N days"},
				paramRelevantOnly: map[string]any{schemaType: schemaTypeBoolean, schemaDefault: false},
			},
			schemaRequired: []string{paramQuery},
		},
		Handler: func(ctx context.Context, args map[string]any) (any, error) {
			q := getStr(args, paramQuery)
			limit := getInt(args, paramLimit, 10)
			var days *int
			if d, ok := args[paramDays]; ok {
				di := toInt(d)
				days = &di
			}
			relevant := getBool(args, paramRelevantOnly)
			return search(ctx, q, limit, days, relevant)
		},
	})
}

func (s *Server) registerTool(tool Tool) {
	s.mu.Lock()
	s.tools[tool.Name] = tool
	s.mu.Unlock()
}

// Start starts the MCP HTTP server.
func (s *Server) Start() error {
	mux := http.NewServeMux()
	mux.HandleFunc("/", s.handleHTTP)
	mux.HandleFunc("/health", s.handleHTTP)
	mux.HandleFunc("/healthz", s.handleHTTP)
	mux.HandleFunc("/readyz", s.handleReady)
	mux.HandleFunc("/mcp", s.handleMCP)

	server := &http.Server{
		Addr:              s.addr,
		Handler:           mux,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      10 * time.Second,
		IdleTimeout:       30 * time.Second,
		ReadHeaderTimeout: 2 * time.Second,
	}

	slog.Info("MCP server starting", "addr", s.addr)
	if err := server.ListenAndServe(); err != nil {
		return fmt.Errorf("mcp server: %w", err)
	}
	return nil
}

// handleHTTP is the liveness endpoint (GET /, /health, /healthz). It proves
// the process is up and the HTTP server is serving; it performs no dependency
// checks. Use /readyz for dependency-gated readiness.
func (s *Server) handleHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/" || r.URL.Path == "/health" || r.URL.Path == "/healthz" {
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(map[string]any{
			"status":  "ok",
			"service": "fantasy-football-engine",
			toolsKey:  len(s.tools),
		}); err != nil {
			slog.Warn("encode health response", "err", err)
		}
		return
	}
	http.NotFound(w, r)
}

// handleReady is the readiness endpoint (GET /readyz). It delegates to the
// health Checker, which pings the daemon's own Postgres pool. When no checker
// is wired it falls back to the liveness response so the endpoint never 5xxs
// purely due to misconfiguration.
func (s *Server) handleReady(w http.ResponseWriter, r *http.Request) {
	if s.checker == nil {
		s.handleHTTP(w, r)
		return
	}
	s.mu.RLock()
	n := len(s.tools)
	s.mu.RUnlock()
	s.checker.Handler(func() int { return n }).ServeHTTP(w, r)
}

// handleMCP handles MCP JSON-RPC requests over HTTP.
// This implements a simplified Streamable HTTP transport:
// - POST with JSON-RPC body → response (JSON or SSE)
// - GET → SSE stream for server-initiated messages (not used currently).
func (s *Server) handleMCP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		JSONRPC string          `json:"jsonrpc"`
		ID      json.RawMessage `json:"id"`
		Method  string          `json:"method"`
		Params  json.RawMessage `json:"params"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONRPCError(w, nil, -32700, "parse error")
		return
	}

	switch req.Method {
	case "initialize":
		writeJSONRPCResult(w, req.ID, map[string]any{
			"protocolVersion": "2025-03-26",
			"capabilities": map[string]any{
				"tools": map[string]any{},
			},
			"serverInfo": map[string]any{
				"name":    "fantasy-football-engine",
				"version": "1.0.0",
			},
		})

	case "tools/list":
		s.mu.RLock()
		tools := make([]map[string]any, 0, len(s.tools))
		for _, tool := range s.tools {
			tools = append(tools, map[string]any{
				"name":        tool.Name,
				"description": tool.Description,
				"inputSchema": tool.InputSchema,
			})
		}
		s.mu.RUnlock()
		writeJSONRPCResult(w, req.ID, map[string]any{"tools": tools})

	case "tools/call":
		var params struct {
			Name      string         `json:"name"`
			Arguments map[string]any `json:"arguments"`
		}
		if err := json.Unmarshal(req.Params, &params); err != nil {
			writeJSONRPCError(w, req.ID, -32602, "invalid params")
			return
		}
		s.mu.RLock()
		tool, ok := s.tools[params.Name]
		s.mu.RUnlock()
		if !ok {
			writeJSONRPCError(w, req.ID, -32601, "tool not found: "+params.Name)
			return
		}
		if missing := missingRequired(tool.InputSchema, params.Arguments); len(missing) > 0 {
			writeJSONRPCError(w, req.ID, -32602, "missing required argument(s): "+strings.Join(missing, ", "))
			return
		}
		result, err := tool.Handler(r.Context(), params.Arguments)
		if err != nil {
			writeJSONRPCResult(w, req.ID, map[string]any{
				"content": []map[string]any{
					{"type": "text", "text": "Error: " + err.Error()},
				},
				"isError": true,
			})
			return
		}
		// Serialize result as text content
		resultJSON, err := json.MarshalIndent(result, "", "  ")
		if err != nil {
			slog.Warn("failed to marshal result", "err", err)
			resultJSON = []byte("(unable to marshal result)")
		}
		writeJSONRPCResult(w, req.ID, map[string]any{
			contentKey: []map[string]any{
				{schemaType: textType, textType: string(resultJSON)},
			},
		})

	default:
		writeJSONRPCError(w, req.ID, -32601, "method not found: "+req.Method)
	}
}

// missingRequired returns the names of any InputSchema "required" fields not
// present in args, so tools/call can reject an incomplete call up front
// instead of letting the handler run with silently-defaulted zero values.
func missingRequired(schema, args map[string]any) []string {
	required, ok := schema["required"].([]string)
	if !ok {
		return nil
	}
	var missing []string
	for _, name := range required {
		if _, ok := args[name]; !ok {
			missing = append(missing, name)
		}
	}
	return missing
}

func writeJSONRPCResult(w http.ResponseWriter, id json.RawMessage, result any) {
	w.Header().Set("Content-Type", contentTypeJSON)
	resp := map[string]any{
		"jsonrpc": jsonrpcVersion,
		"id":      id,
		"result":  result,
	}
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		slog.Warn("failed to encode JSON-RPC result", "err", err)
	}
}

func writeJSONRPCError(w http.ResponseWriter, id json.RawMessage, code int, message string) {
	w.Header().Set("Content-Type", contentTypeJSON)
	if id == nil {
		id = json.RawMessage("null")
	}
	resp := map[string]any{
		"jsonrpc": jsonrpcVersion,
		"id":      id,
		"error": map[string]any{
			"code":    code,
			"message": message,
		},
	}
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		slog.Warn("failed to encode JSON-RPC error", "err", err)
	}
}

// --- Helpers ---

const (
	rankingsSrcFantasyCalc  = "FantasyCalc"
	rankingsSrcDynastyDaddy = "Dynasty Daddy"
	rankingsSrcKeepTradeCut = "KeepTradeCut"
	rankingsSrcFantasyPros  = "FantasyPros ECR"
	rankingsDefaultMarket   = 14
)

// rankingsSourceMarket maps a rankings source name to its default market.
var rankingsSourceMarket = map[string]int{
	rankingsSrcFantasyCalc:  1,
	rankingsSrcDynastyDaddy: 14,
	rankingsSrcKeepTradeCut: 0,
	rankingsSrcFantasyPros:  1,
}

// rankingsMarketSource maps a market id to its canonical source name.
// When multiple sources share a market (FantasyCalc and FantasyPros ECR both
// use market 1), the first registered source wins; callers wanting the other
// source must pass source explicitly.
var rankingsMarketSource = map[int]string{
	0:  rankingsSrcKeepTradeCut,
	14: rankingsSrcDynastyDaddy,
	1:  rankingsSrcFantasyCalc,
	2:  rankingsSrcFantasyCalc,
	3:  rankingsSrcFantasyCalc,
}

// resolveRankingsSourceMarket derives the (source, market) pair from the
// tool arguments using these rules:
//   - Both given: use as-is.
//   - Only source: derive market from rankingsSourceMarket.
//   - Only market: derive source from rankingsMarketSource.
//   - Neither: default to Dynasty Daddy, market 14.
//
// Returns (source, market, true) on success, or ("", 0, false) if an
// explicitly-passed source or market is unknown.
func resolveRankingsSourceMarket(args map[string]any) (string, int, bool) {
	srcArg, hasSrc := args["source"]
	mktArg, hasMkt := args["market"]

	source := ""
	if hasSrc && srcArg != nil {
		source = fmt.Sprint(srcArg)
	}
	market := -1
	if hasMkt && mktArg != nil {
		market = toInt(mktArg)
	}

	switch {
	case source != "" && market >= 0:
		// Both explicit — validate.
		if _, ok := rankingsSourceMarket[source]; !ok {
			return "", 0, false
		}
		return source, market, true
	case source != "":
		// Source only — derive market.
		m, ok := rankingsSourceMarket[source]
		if !ok {
			return "", 0, false
		}
		return source, m, true
	case market >= 0:
		// Market only — derive source.
		s, ok := rankingsMarketSource[market]
		if !ok {
			return "", 0, false
		}
		return s, market, true
	default:
		// Neither — default.
		return rankingsSrcDynastyDaddy, rankingsDefaultMarket, true
	}
}

func getStr(m map[string]any, key string) string {
	v, ok := m[key]
	if !ok || v == nil {
		return ""
	}
	return fmt.Sprint(v)
}

func getInt(m map[string]any, key string, def int) int {
	v, ok := m[key]
	if !ok || v == nil {
		return def
	}
	return toInt(v)
}

func getBool(m map[string]any, key string) bool {
	v, ok := m[key]
	if !ok || v == nil {
		return false
	}
	switch val := v.(type) {
	case bool:
		return val
	default:
		return false
	}
}

func toInt(v any) int {
	switch val := v.(type) {
	case float64:
		return int(val)
	case int:
		return val
	default:
		return 0
	}
}

func toStrSlice(v any) []string {
	if v == nil {
		return nil
	}
	arr, ok := v.([]any)
	if !ok {
		return nil
	}
	result := make([]string, 0, len(arr))
	for _, item := range arr {
		result = append(result, fmt.Sprint(item))
	}
	return result
}
