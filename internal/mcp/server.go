package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"sync"

	"github.com/markis/fantasy-football-engine/internal/pipeline"
	"github.com/markis/fantasy-football-engine/internal/query"
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
	InputSchema map[string]interface{}
	Handler     func(ctx context.Context, args map[string]interface{}) (interface{}, error)
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
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"query":         map[string]interface{}{"type": "string", "description": "Search query"},
				"limit":         map[string]interface{}{"type": "integer", "default": 10},
				"days":          map[string]interface{}{"type": "integer", "description": "Only items from last N days"},
				"relevant_only": map[string]interface{}{"type": "boolean", "default": false},
			},
			"required": []string{"query"},
		},
		Handler: func(ctx context.Context, args map[string]interface{}) (interface{}, error) {
			query := getStr(args, "query")
			limit := getInt(args, "limit", 10)
			var days *int
			if d, ok := args["days"]; ok {
				di := toInt(d)
				days = &di
			}
			relevant := getBool(args, "relevant_only", false)
			return s.query.SearchNews(ctx, query, limit, days, relevant)
		},
	})

	s.registerTool(Tool{
		Name:        "get_stories",
		Description: "Get top story clusters from a time window.",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"hours": map[string]interface{}{"type": "integer", "default": 24},
				"limit": map[string]interface{}{"type": "integer", "default": 5},
			},
		},
		Handler: func(ctx context.Context, args map[string]interface{}) (interface{}, error) {
			hours := getInt(args, "hours", 24)
			limit := getInt(args, "limit", 5)
			return s.query.GetStories(ctx, hours, limit)
		},
	})

	s.registerTool(Tool{
		Name:        "search_facts",
		Description: "Semantic search over extracted fantasy football facts.",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"query": map[string]interface{}{"type": "string"},
				"limit": map[string]interface{}{"type": "integer", "default": 10},
			},
			"required": []string{"query"},
		},
		Handler: func(ctx context.Context, args map[string]interface{}) (interface{}, error) {
			q := getStr(args, "query")
			limit := getInt(args, "limit", 10)
			return s.query.SearchFacts(ctx, q, limit)
		},
	})

	s.registerTool(Tool{
		Name:        "get_recent_news",
		Description: "Get the latest N news items, optionally filtered to fantasy-relevant only.",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"limit":         map[string]interface{}{"type": "integer", "default": 10},
				"relevant_only": map[string]interface{}{"type": "boolean", "default": false},
			},
		},
		Handler: func(ctx context.Context, args map[string]interface{}) (interface{}, error) {
			limit := getInt(args, "limit", 10)
			relevant := getBool(args, "relevant_only", false)
			return s.query.GetRecentNews(ctx, limit, relevant)
		},
	})

	// Players & valuations
	s.registerTool(Tool{
		Name:        "search_players",
		Description: "Search NFL players by name, with optional position filter.",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"query":    map[string]interface{}{"type": "string"},
				"position": map[string]interface{}{"type": "string"},
				"limit":    map[string]interface{}{"type": "integer", "default": 25},
			},
			"required": []string{"query"},
		},
		Handler: func(ctx context.Context, args map[string]interface{}) (interface{}, error) {
			q := getStr(args, "query")
			limit := getInt(args, "limit", 25)
			var pos *string
			if p := getStr(args, "position"); p != "" {
				pos = &p
			}
			return s.query.SearchPlayers(ctx, q, pos, limit)
		},
	})

	s.registerTool(Tool{
		Name:        "get_player",
		Description: "Get a full player profile by Sleeper player ID.",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"player_id": map[string]interface{}{"type": "string"},
			},
			"required": []string{"player_id"},
		},
		Handler: func(ctx context.Context, args map[string]interface{}) (interface{}, error) {
			pid := getStr(args, "player_id")
			return s.query.GetPlayer(ctx, pid)
		},
	})

	s.registerTool(Tool{
		Name:        "get_rankings",
		Description: "Get dynasty trade value rankings. Default source: FantasyCalc, market 14 (Dynasty Daddy composite).",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"position":  map[string]interface{}{"type": "string"},
				"limit":     map[string]interface{}{"type": "integer", "default": 15},
				"source":    map[string]interface{}{"type": "string", "default": "FantasyCalc"},
				"market":    map[string]interface{}{"type": "integer", "default": 14},
				"superflex": map[string]interface{}{"type": "boolean", "default": false},
			},
		},
		Handler: func(ctx context.Context, args map[string]interface{}) (interface{}, error) {
			limit := getInt(args, "limit", 15)
			source := getStr(args, "source")
			if source == "" {
				source = "FantasyCalc"
			}
			market := getInt(args, "market", 14)
			superflex := getBool(args, "superflex", false)
			var pos *string
			if p := getStr(args, "position"); p != "" {
				pos = &p
			}
			return s.query.GetRankings(ctx, pos, limit, source, market, superflex)
		},
	})

	s.registerTool(Tool{
		Name:        "get_trending_players",
		Description: "Get trending players (adds/drops) from Sleeper.",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"trend_type": map[string]interface{}{"type": "string", "default": "add"},
				"limit":      map[string]interface{}{"type": "integer", "default": 25},
			},
		},
		Handler: func(ctx context.Context, args map[string]interface{}) (interface{}, error) {
			tt := getStr(args, "trend_type")
			limit := getInt(args, "limit", 25)
			return s.query.GetTrendingPlayers(ctx, tt, limit)
		},
	})

	s.registerTool(Tool{
		Name:        "get_free_agents",
		Description: "Get top ranked free agents (unowned players) in a league.",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"league_id": map[string]interface{}{"type": "string"},
				"position":  map[string]interface{}{"type": "string"},
				"limit":     map[string]interface{}{"type": "integer", "default": 15},
				"superflex": map[string]interface{}{"type": "boolean", "default": false},
			},
			"required": []string{"league_id"},
		},
		Handler: func(ctx context.Context, args map[string]interface{}) (interface{}, error) {
			leagueID := getStr(args, "league_id")
			limit := getInt(args, "limit", 15)
			superflex := getBool(args, "superflex", false)
			var pos *string
			if p := getStr(args, "position"); p != "" {
				pos = &p
			}
			return s.query.GetFreeAgents(ctx, leagueID, pos, limit, superflex)
		},
	})

	// Leagues
	s.registerTool(Tool{
		Name:        "get_nfl_state",
		Description: "Get the current NFL state (week, season, season type).",
		InputSchema: map[string]interface{}{"type": "object"},
		Handler: func(ctx context.Context, args map[string]interface{}) (interface{}, error) {
			return s.query.GetNFLState(ctx)
		},
	})

	// Team & assessment
	s.registerTool(Tool{
		Name:        "evaluate_roster",
		Description: "Get structured roster data with trade values for a user in a league. Returns data only — the agent does the reasoning.",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"league_id": map[string]interface{}{"type": "string"},
				"user_id":   map[string]interface{}{"type": "string", "default": "558115100726579200"},
				"superflex": map[string]interface{}{"type": "boolean", "default": false},
			},
			"required": []string{"league_id"},
		},
		Handler: func(ctx context.Context, args map[string]interface{}) (interface{}, error) {
			leagueID := getStr(args, "league_id")
			userID := getStr(args, "user_id")
			if userID == "" {
				userID = "558115100726579200"
			}
			superflex := getBool(args, "superflex", false)
			return s.query.EvaluateRoster(ctx, leagueID, userID, superflex)
		},
	})

	s.registerTool(Tool{
		Name:        "evaluate_trade",
		Description: "Evaluate a dynasty trade proposal. Returns structured data: both sides' players with trade values, totals, delta, and a recommendation. The agent does the reasoning.",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"give":      map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "string"}},
				"get":       map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "string"}},
				"league_id": map[string]interface{}{"type": "string"},
				"superflex": map[string]interface{}{"type": "boolean", "default": false},
			},
			"required": []string{"give", "get"},
		},
		Handler: func(ctx context.Context, args map[string]interface{}) (interface{}, error) {
			give := toStrSlice(args["give"])
			get := toStrSlice(args["get"])
			leagueID := getStr(args, "league_id")
			superflex := getBool(args, "superflex", false)
			return s.query.EvaluateTrade(ctx, give, get, leagueID, superflex)
		},
	})

	// Self-study
	s.registerTool(Tool{
		Name:        "get_study_material",
		Description: "Get a digest of recent stories + news for agent self-study. Returns markdown + structured data.",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"hours": map[string]interface{}{"type": "integer", "default": 24},
			},
		},
		Handler: func(ctx context.Context, args map[string]interface{}) (interface{}, error) {
			hours := getInt(args, "hours", 24)
			return s.query.GetStudyMaterial(ctx, hours)
		},
	})

	// Pipeline control
	s.registerTool(Tool{
		Name:        "trigger_pipeline",
		Description: "Manually trigger a named pipeline step (e.g. fetch_rss, enrich, publish_daily). Requires pipeline trigger to be enabled.",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"step": map[string]interface{}{"type": "string", "description": "Pipeline step name"},
			},
			"required": []string{"step"},
		},
		Handler: func(ctx context.Context, args map[string]interface{}) (interface{}, error) {
			if s.trigger == nil {
				return nil, fmt.Errorf("pipeline trigger not enabled")
			}
			step := getStr(args, "step")
			if err := s.trigger(ctx, step); err != nil {
				return nil, err
			}
			return map[string]interface{}{"status": "ok", "step": step}, nil
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

	slog.Info("MCP server starting", "addr", s.addr)
	return http.ListenAndServe(s.addr, mux)
}

// handleHTTP is a simple health check endpoint.
func (s *Server) handleHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/" || r.URL.Path == "/health" {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"status":  "ok",
			"service": "fantasy-football-engine",
			"tools":   len(s.tools),
		})
		return
	}
	http.NotFound(w, r)
}

// handleMCP handles MCP JSON-RPC requests over HTTP.
// This implements a simplified Streamable HTTP transport:
// - POST with JSON-RPC body → response (JSON or SSE)
// - GET → SSE stream for server-initiated messages (not used currently)
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
		writeJSONRPCResult(w, req.ID, map[string]interface{}{
			"protocolVersion": "2025-03-26",
			"capabilities": map[string]interface{}{
				"tools": map[string]interface{}{},
			},
			"serverInfo": map[string]interface{}{
				"name":    "fantasy-football-engine",
				"version": "1.0.0",
			},
		})

	case "tools/list":
		s.mu.RLock()
		tools := make([]map[string]interface{}, 0, len(s.tools))
		for _, tool := range s.tools {
			tools = append(tools, map[string]interface{}{
				"name":        tool.Name,
				"description": tool.Description,
				"inputSchema": tool.InputSchema,
			})
		}
		s.mu.RUnlock()
		writeJSONRPCResult(w, req.ID, map[string]interface{}{"tools": tools})

	case "tools/call":
		var params struct {
			Name      string                 `json:"name"`
			Arguments map[string]interface{} `json:"arguments"`
		}
		if err := json.Unmarshal(req.Params, &params); err != nil {
			writeJSONRPCError(w, req.ID, -32602, "invalid params")
			return
		}
		s.mu.RLock()
		tool, ok := s.tools[params.Name]
		s.mu.RUnlock()
		if !ok {
			writeJSONRPCError(w, req.ID, -32601, fmt.Sprintf("tool not found: %s", params.Name))
			return
		}
		result, err := tool.Handler(r.Context(), params.Arguments)
		if err != nil {
			writeJSONRPCResult(w, req.ID, map[string]interface{}{
				"content": []map[string]interface{}{
					{"type": "text", "text": fmt.Sprintf("Error: %s", err.Error())},
				},
				"isError": true,
			})
			return
		}
		// Serialize result as text content
		resultJSON, _ := json.MarshalIndent(result, "", "  ")
		writeJSONRPCResult(w, req.ID, map[string]interface{}{
			"content": []map[string]interface{}{
				{"type": "text", "text": string(resultJSON)},
			},
		})

	default:
		writeJSONRPCError(w, req.ID, -32601, fmt.Sprintf("method not found: %s", req.Method))
	}
}

func writeJSONRPCResult(w http.ResponseWriter, id json.RawMessage, result interface{}) {
	w.Header().Set("Content-Type", "application/json")
	resp := map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      json.RawMessage(id),
		"result":  result,
	}
	json.NewEncoder(w).Encode(resp)
}

func writeJSONRPCError(w http.ResponseWriter, id json.RawMessage, code int, message string) {
	w.Header().Set("Content-Type", "application/json")
	if id == nil {
		id = json.RawMessage("null")
	}
	resp := map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      json.RawMessage(id),
		"error": map[string]interface{}{
			"code":    code,
			"message": message,
		},
	}
	json.NewEncoder(w).Encode(resp)
}

// --- Helpers ---

func getStr(m map[string]interface{}, key string) string {
	v, ok := m[key]
	if !ok || v == nil {
		return ""
	}
	return fmt.Sprint(v)
}

func getInt(m map[string]interface{}, key string, def int) int {
	v, ok := m[key]
	if !ok || v == nil {
		return def
	}
	return toInt(v)
}

func getBool(m map[string]interface{}, key string, def bool) bool {
	v, ok := m[key]
	if !ok || v == nil {
		return def
	}
	switch val := v.(type) {
	case bool:
		return val
	default:
		return false
	}
}

func toInt(v interface{}) int {
	switch val := v.(type) {
	case float64:
		return int(val)
	case int:
		return val
	default:
		return 0
	}
}

func toStrSlice(v interface{}) []string {
	if v == nil {
		return nil
	}
	arr, ok := v.([]interface{})
	if !ok {
		return nil
	}
	result := make([]string, 0, len(arr))
	for _, item := range arr {
		result = append(result, fmt.Sprint(item))
	}
	return result
}

// Ensure pipeline import is used
var _ = pipeline.SimhashCompute