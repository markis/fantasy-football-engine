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

	"github.com/markis/fantasy-football-engine/internal/query"
)

var errPipelineTriggerNotEnabled = errors.New("pipeline trigger not enabled")

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
	paramStep         = "step"
	paramQuery        = "query"
	paramLimit        = "limit"
)

// Server is the MCP server that exposes tools over Streamable HTTP.
// It implements a minimal MCP-compatible JSON-RPC handler that supports
// tools/list and tools/call.
type Server struct {
	query   *query.Service
	addr    string
	tools   map[string]Tool
	mu      sync.RWMutex
	trigger func(ctx context.Context, step string) error
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

// registerTools registers all MCP tools.
func (s *Server) registerTools() {
	// News & stories
	s.registerTool(Tool{
		Name:        "search_news",
		Description: "Semantic search over fantasy football news items using embeddings.",
		InputSchema: map[string]any{
			schemaType: schemaTypeObject,
			schemaProperties: map[string]any{
				paramQuery:      map[string]any{schemaType: schemaTypeString, schemaDescription: "Search query"},
				paramLimit:      map[string]any{schemaType: schemaTypeInteger, schemaDefault: 10},
				"days":          map[string]any{schemaType: schemaTypeInteger, schemaDescription: "Only items from last N days"},
				"relevant_only": map[string]any{schemaType: schemaTypeBoolean, schemaDefault: false},
			},
			schemaRequired: []string{"query"},
		},
		Handler: func(ctx context.Context, args map[string]any) (any, error) {
			q := getStr(args, paramQuery)
			limit := getInt(args, paramLimit, 10)
			var days *int
			if d, ok := args["days"]; ok {
				di := toInt(d)
				days = &di
			}
			relevant := getBool(args, "relevant_only")
			return s.query.SearchNews(ctx, q, limit, days, relevant)
		},
	})

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
		Name:        "get_rankings",
		Description: "Get dynasty trade value rankings. Default source: FantasyCalc, market 14 (Dynasty Daddy composite).",
		InputSchema: map[string]any{
			schemaType: schemaTypeObject,
			schemaProperties: map[string]any{
				"position":     map[string]any{"type": "string"},
				"limit":        map[string]any{"type": "integer", "default": 15},
				"source":       map[string]any{"type": "string", "default": "FantasyCalc"},
				"market":       map[string]any{"type": "integer", "default": 14},
				paramSuperflex: map[string]any{schemaType: schemaTypeBoolean, schemaDefault: false},
			},
		},
		Handler: func(ctx context.Context, args map[string]any) (any, error) {
			limit := getInt(args, "limit", 15)
			source := getStr(args, "source")
			if source == "" {
				source = "FantasyCalc"
			}
			market := getInt(args, "market", 14)
			superflex := getBool(args, paramSuperflex)
			var pos *string
			if p := getStr(args, paramPosition); p != "" {
				pos = &p
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
				"trend_type": map[string]any{"type": "string", "default": "add"},
				"limit":      map[string]any{"type": "integer", "default": 25},
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
				"position":     map[string]any{"type": "string"},
				"limit":        map[string]any{"type": "integer", "default": 15},
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
		InputSchema: map[string]any{"type": "object"},
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
				"user_id":      map[string]any{"type": "string", "default": "558115100726579200"},
				paramSuperflex: map[string]any{schemaType: schemaTypeBoolean, schemaDefault: false},
			},
			schemaRequired: []string{paramLeagueID},
		},
		Handler: func(ctx context.Context, args map[string]any) (any, error) {
			leagueID := getStr(args, paramLeagueID)
			userID := getStr(args, "user_id")
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
				"give":         map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
				"get":          map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
				paramLeagueID:  map[string]any{schemaType: schemaTypeString},
				paramSuperflex: map[string]any{schemaType: schemaTypeBoolean, schemaDefault: false},
			},
			"required": []string{"give", "get"},
		},
		Handler: func(ctx context.Context, args map[string]any) (any, error) {
			give := toStrSlice(args["give"])
			get := toStrSlice(args["get"])
			leagueID := getStr(args, paramLeagueID)
			superflex := getBool(args, paramSuperflex)
			return s.query.EvaluateTrade(ctx, give, get, leagueID, superflex)
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

func (s *Server) registerTool(tool Tool) {
	s.mu.Lock()
	s.tools[tool.Name] = tool
	s.mu.Unlock()
}

// Start starts the MCP HTTP server.
func (s *Server) Start() error {
	mux := http.NewServeMux()
	mux.HandleFunc("/", s.handleHTTP)
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
	return server.ListenAndServe()
}

// handleHTTP is a simple health check endpoint.
func (s *Server) handleHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/" || r.URL.Path == "/health" {
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
