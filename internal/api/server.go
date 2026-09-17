package api

import (
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"log-download-portal/internal/agent"
	"log-download-portal/internal/audit"
	"log-download-portal/internal/auth"
	"log-download-portal/internal/config"
	"log-download-portal/internal/discovery"
	"log-download-portal/internal/security"
)

type Dependencies struct {
	Config    *config.Config
	Sessions  *auth.SessionManager
	Discovery discovery.Resolver
	Agent     *agent.Client
	Auditor   audit.Logger
	Assets    fs.FS
}

type Server struct {
	cfg            *config.Config
	sessions       *auth.SessionManager
	discovery      discovery.Resolver
	agent          *agent.Client
	auditor        audit.Logger
	assets         fs.FS
	trustedProxies []*net.IPNet
}

func NewServer(deps Dependencies) http.Handler {
	trustedProxies := make([]*net.IPNet, 0, len(deps.Config.Server.TrustedProxyCIDRs))
	for _, cidr := range deps.Config.Server.TrustedProxyCIDRs {
		_, network, err := net.ParseCIDR(cidr)
		if err != nil {
			panic(err)
		}
		trustedProxies = append(trustedProxies, network)
	}
	s := &Server{
		cfg:            deps.Config,
		sessions:       deps.Sessions,
		discovery:      deps.Discovery,
		agent:          deps.Agent,
		auditor:        deps.Auditor,
		assets:         deps.Assets,
		trustedProxies: trustedProxies,
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.healthz)
	mux.HandleFunc("GET /readyz", s.readyz)
	mux.HandleFunc("POST /api/v1/login", s.login)
	mux.HandleFunc("POST /api/v1/logout", s.requireSession(s.logout))
	mux.HandleFunc("GET /api/v1/session", s.requireSession(s.session))
	mux.HandleFunc("GET /api/v1/nodes", s.requireSession(s.nodes))
	mux.HandleFunc("GET /api/v1/nodes/", s.requireSession(s.nodeRoutes))
	mux.Handle("/", http.FileServerFS(s.assets))
	return secureHeaders(s.withRequestID(mux))
}

func secureHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "same-origin")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self'; connect-src 'self'; form-action 'self'; object-src 'none'; base-uri 'none'; frame-ancestors 'none'")
		next.ServeHTTP(w, r)
	})
}

func (s *Server) healthz(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) readyz(w http.ResponseWriter, r *http.Request) {
	if err := s.discovery.Ready(r.Context()); err != nil {
		writeError(w, r, http.StatusServiceUnavailable, "DISCOVERY_NOT_READY", "集群状态暂时不可用")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&body); err != nil {
		writeError(w, r, http.StatusBadRequest, "BAD_REQUEST", "请求格式不正确")
		return
	}
	err := s.sessions.Authenticate(r.RemoteAddr, body.Username, body.Password)
	success := err == nil
	reason := ""
	status := http.StatusOK
	if errors.Is(err, auth.ErrRateLimited) {
		status = http.StatusTooManyRequests
		reason = "rate_limited"
	} else if err != nil {
		status = http.StatusUnauthorized
		reason = "invalid_credentials"
	}
	s.auditor.Log(audit.Event{
		"eventType": "login",
		"requestId": requestIDFrom(r.Context()),
		"username":  body.Username,
		"clientIP":  s.clientIP(r),
		"success":   success,
		"reason":    reason,
	})
	if !success {
		writeError(w, r, status, "LOGIN_FAILED", "用户名或密码不正确")
		return
	}
	s.sessions.Create(w, body.Username)
	writeJSON(w, http.StatusOK, map[string]any{"username": body.Username})
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request, username string) {
	s.sessions.Clear(w, r)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) session(w http.ResponseWriter, r *http.Request, username string) {
	writeJSON(w, http.StatusOK, map[string]any{"username": username})
}

func (s *Server) nodes(w http.ResponseWriter, r *http.Request, username string) {
	nodes, err := s.discovery.Nodes(r.Context())
	if err != nil {
		s.discoveryError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": nodes})
}

func (s *Server) nodeRoutes(w http.ResponseWriter, r *http.Request, username string) {
	parts := strings.Split(strings.TrimPrefix(r.URL.EscapedPath(), "/api/v1/nodes/"), "/")
	if len(parts) == 2 && parts[1] == "services" && r.Method == http.MethodGet {
		s.services(w, r, parts[0], username)
		return
	}
	if len(parts) == 4 && parts[1] == "services" && parts[3] == "files" && r.Method == http.MethodGet {
		s.files(w, r, parts[0], parts[2], username)
		return
	}
	if len(parts) == 6 && parts[1] == "services" && parts[3] == "files" && parts[5] == "download" && r.Method == http.MethodGet {
		s.download(w, r, parts[0], parts[2], parts[4], username)
		return
	}
	writeError(w, r, http.StatusNotFound, "NOT_FOUND", "接口不存在")
}

func (s *Server) services(w http.ResponseWriter, r *http.Request, rawNode, username string) {
	nodeName, ok := decodeAndValidateNode(w, r, rawNode)
	if !ok {
		return
	}
	nodes, err := s.discovery.Nodes(r.Context())
	if err != nil {
		s.discoveryError(w, r, err)
		return
	}
	var selected *discovery.Node
	for _, node := range nodes {
		if node.Name == nodeName {
			n := node
			selected = &n
			break
		}
	}
	if selected == nil {
		writeError(w, r, http.StatusNotFound, "NODE_NOT_FOUND", "节点不存在")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"node":     selected,
		"services": s.cfg.Services,
	})
}

func (s *Server) files(w http.ResponseWriter, r *http.Request, rawNode, rawService, username string) {
	nodeName, service, ok := s.decodeNodeService(w, r, rawNode, rawService)
	if !ok {
		return
	}
	endpoint, err := s.discovery.ResolveAgent(r.Context(), nodeName)
	if err != nil {
		s.discoveryError(w, r, err)
		return
	}
	files, err := s.agent.ListFiles(r.Context(), endpoint.BaseURL, service.Directory)
	if err != nil {
		s.agentError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": files})
}

func (s *Server) download(w http.ResponseWriter, r *http.Request, rawNode, rawService, rawFile, username string) {
	start := time.Now()
	nodeName, service, ok := s.decodeNodeService(w, r, rawNode, rawService)
	if !ok {
		return
	}
	filename, err := security.DecodePathSegment(rawFile)
	if err != nil || security.ValidateFileName(filename) != nil {
		s.securityReject(r, username, "invalid_filename")
		writeError(w, r, http.StatusBadRequest, "BAD_FILENAME", "文件名不合法")
		return
	}
	rangeHeader := r.Header.Get("Range")
	if err := security.ValidateRangeHeader(rangeHeader); err != nil {
		s.securityReject(r, username, "invalid_range")
		writeError(w, r, http.StatusBadRequest, "BAD_RANGE", "Range 请求不合法")
		return
	}
	endpoint, err := s.discovery.ResolveAgent(r.Context(), nodeName)
	if err != nil {
		s.discoveryError(w, r, err)
		return
	}
	resp, err := s.agent.NewDownloadRequest(r.Context(), endpoint.BaseURL, service.Directory, filename, rangeHeader)
	if err != nil {
		s.agentError(w, r, err)
		s.auditDownload(r, username, nodeName, service.ID, filename, 0, 0, false, start)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		writeError(w, r, http.StatusNotFound, "FILE_NOT_FOUND", "文件已不存在")
		s.auditDownload(r, username, nodeName, service.ID, filename, http.StatusNotFound, 0, false, start)
		return
	}

	copyDownloadHeaders(w.Header(), resp.Header)
	w.Header().Set("Content-Disposition", security.ContentDisposition(filename))
	w.WriteHeader(resp.StatusCode)
	bytes, copyErr := s.agent.CopyDownload(w, resp.Body)
	if flusher, ok := w.(http.Flusher); ok {
		flusher.Flush()
	}
	completed := copyErr == nil
	s.auditDownload(r, username, nodeName, service.ID, filename, resp.StatusCode, bytes, completed, start)
}

func (s *Server) decodeNodeService(w http.ResponseWriter, r *http.Request, rawNode, rawService string) (string, config.ServiceConfig, bool) {
	nodeName, ok := decodeAndValidateNode(w, r, rawNode)
	if !ok {
		return "", config.ServiceConfig{}, false
	}
	serviceID, err := security.DecodePathSegment(rawService)
	if err != nil || security.ValidateServiceID(serviceID) != nil {
		s.securityReject(r, "", "invalid_service")
		writeError(w, r, http.StatusBadRequest, "BAD_SERVICE", "服务参数不合法")
		return "", config.ServiceConfig{}, false
	}
	service, exists := s.cfg.ServiceByID(serviceID)
	if !exists {
		writeError(w, r, http.StatusNotFound, "SERVICE_NOT_FOUND", "服务不存在")
		return "", config.ServiceConfig{}, false
	}
	return nodeName, service, true
}

func decodeAndValidateNode(w http.ResponseWriter, r *http.Request, rawNode string) (string, bool) {
	nodeName, err := security.DecodePathSegment(rawNode)
	if err != nil || security.ValidateNodeName(nodeName) != nil {
		writeError(w, r, http.StatusBadRequest, "BAD_NODE", "节点参数不合法")
		return "", false
	}
	return nodeName, true
}

func (s *Server) requireSession(next func(http.ResponseWriter, *http.Request, string)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		username, ok := s.sessions.User(r)
		if !ok {
			writeError(w, r, http.StatusUnauthorized, "UNAUTHORIZED", "登录状态已失效，请重新登录")
			return
		}
		next(w, r, username)
	}
}

func (s *Server) discoveryError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, discovery.ErrNoNode):
		writeError(w, r, http.StatusNotFound, "NODE_NOT_FOUND", "节点不存在")
	case errors.Is(err, discovery.ErrNoAgent):
		writeError(w, r, http.StatusServiceUnavailable, "AGENT_UNAVAILABLE", "节点日志代理不可用")
	default:
		writeError(w, r, http.StatusServiceUnavailable, "DISCOVERY_UNAVAILABLE", "集群状态暂时不可用")
	}
}

func (s *Server) agentError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, agent.ErrListTooLarge):
		writeError(w, r, http.StatusRequestEntityTooLarge, "FILE_LIST_TOO_LARGE", "文件数量过多，无法展示")
	case errors.Is(err, os.ErrNotExist):
		writeError(w, r, http.StatusNotFound, "FILE_NOT_FOUND", "文件已不存在")
	case errors.Is(err, agent.ErrBadAgentReply):
		writeError(w, r, http.StatusBadGateway, "UPSTREAM_ERROR", "节点日志代理响应异常")
	default:
		writeError(w, r, http.StatusGatewayTimeout, "UPSTREAM_TIMEOUT", "节点响应超时，请稍后重试")
	}
}

func (s *Server) securityReject(r *http.Request, username, reason string) {
	s.auditor.Log(audit.Event{
		"eventType": "security",
		"requestId": requestIDFrom(r.Context()),
		"username":  username,
		"clientIP":  s.clientIP(r),
		"reason":    reason,
	})
}

func (s *Server) auditDownload(r *http.Request, username, node, service, filename string, status int, bytes int64, completed bool, start time.Time) {
	s.auditor.Log(audit.Event{
		"eventType":        "download",
		"requestId":        requestIDFrom(r.Context()),
		"username":         username,
		"clientIP":         s.clientIP(r),
		"node":             node,
		"service":          service,
		"filename":         filename,
		"httpStatus":       status,
		"bytesTransferred": bytes,
		"durationMs":       time.Since(start).Milliseconds(),
		"completed":        completed,
	})
}

func copyDownloadHeaders(dst, src http.Header) {
	for _, key := range []string{
		"Accept-Ranges",
		"Content-Length",
		"Content-Range",
		"Content-Type",
		"ETag",
		"Last-Modified",
	} {
		if value := src.Get(key); value != "" {
			dst.Set(key, value)
		}
	}
}

func (s *Server) clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	remoteIP := net.ParseIP(host)
	if remoteIP == nil || !s.isTrustedProxy(remoteIP) {
		return host
	}
	forwarded := strings.TrimSpace(strings.Split(r.Header.Get("X-Forwarded-For"), ",")[0])
	if parsed := net.ParseIP(forwarded); parsed != nil {
		return parsed.String()
	}
	return host
}

func (s *Server) isTrustedProxy(ip net.IP) bool {
	for _, network := range s.trustedProxies {
		if network.Contains(ip) {
			return true
		}
	}
	return false
}
