package handlers

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"squidadmin/backend/internal/squid"
)

// AccessHandler serves ACL objects, the ordered access rules, the effective
// policy and the "why was this request allowed/blocked?" tester.
type AccessHandler struct {
	access *squid.AccessManager
	mgr    *squid.Manager
	env    squid.PolicyEnv
}

func NewAccessHandler(access *squid.AccessManager, mgr *squid.Manager, env squid.PolicyEnv) *AccessHandler {
	return &AccessHandler{access: access, mgr: mgr, env: env}
}

func respondAccessError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, squid.ErrNotFound):
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
	case errors.Is(err, squid.ErrACLInUse):
		c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
	default:
		c.JSON(http.StatusUnprocessableEntity, gin.H{"error": err.Error()})
	}
}

func pathID(c *gin.Context) (int64, bool) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return 0, false
	}
	return id, true
}

// withReload adds the outcome of reloading squid to a response, like every
// other handler that changes squid.conf.
func withReload(mgr *squid.Manager, resp gin.H) gin.H {
	resp["reloaded"] = true
	if err := mgr.Reconfigure(); err != nil {
		resp["reloaded"] = false
		resp["reload_error"] = err.Error()
	}
	return resp
}

func (h *AccessHandler) Types(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"types": squid.ACLTypes()})
}

// ----------------------------------------------------------------- ACLs

func (h *AccessHandler) ListACLs(c *gin.Context) {
	acls, err := h.access.ListACLs()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"acls": acls})
}

type createACLRequest struct {
	Name            string   `json:"name" binding:"required"`
	Type            string   `json:"type" binding:"required"`
	Values          []string `json:"values"`
	CaseInsensitive *bool    `json:"case_insensitive"`
	Description     string   `json:"description"`
}

func (h *AccessHandler) CreateACL(c *gin.Context) {
	var req createACLRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "name and type are required"})
		return
	}
	ci := true
	if req.CaseInsensitive != nil {
		ci = *req.CaseInsensitive
	}
	acl, err := h.access.CreateACL(req.Name, req.Type, req.Values, ci, req.Description)
	if err != nil {
		respondAccessError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"acl": acl})
}

type updateACLRequest struct {
	Values          []string `json:"values"`
	CaseInsensitive *bool    `json:"case_insensitive"`
	Description     *string  `json:"description"`
}

func (h *AccessHandler) UpdateACL(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	var req updateACLRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
		return
	}
	acl, err := h.access.UpdateACL(id, req.Values, req.CaseInsensitive, req.Description)
	if err != nil {
		respondAccessError(c, err)
		return
	}
	c.JSON(http.StatusOK, withReload(h.mgr, gin.H{"acl": acl}))
}

func (h *AccessHandler) DeleteACL(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	if err := h.access.DeleteACL(id); err != nil {
		respondAccessError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "deleted"})
}

// ---------------------------------------------------------------- rules

func (h *AccessHandler) ListRules(c *gin.Context) {
	rules, err := h.access.ListRules()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"rules": rules})
}

type createRuleRequest struct {
	Action   string           `json:"action" binding:"required"`
	Terms    []squid.RuleTerm `json:"terms"`
	Comment  string           `json:"comment"`
	Position int              `json:"position"`
	Enabled  *bool            `json:"enabled"`
}

func (h *AccessHandler) CreateRule(c *gin.Context) {
	var req createRuleRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "action and terms are required"})
		return
	}
	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	rule, err := h.access.CreateRule(req.Action, req.Terms, req.Comment, req.Position, enabled)
	if err != nil {
		respondAccessError(c, err)
		return
	}
	c.JSON(http.StatusOK, withReload(h.mgr, gin.H{"rule": rule}))
}

func (h *AccessHandler) UpdateRule(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	var req squid.RuleUpdate
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
		return
	}
	rule, err := h.access.UpdateRule(id, req)
	if err != nil {
		respondAccessError(c, err)
		return
	}
	c.JSON(http.StatusOK, withReload(h.mgr, gin.H{"rule": rule}))
}

func (h *AccessHandler) DeleteRule(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	if err := h.access.DeleteRule(id); err != nil {
		respondAccessError(c, err)
		return
	}
	c.JSON(http.StatusOK, withReload(h.mgr, gin.H{"status": "deleted"}))
}

type reorderRequest struct {
	IDs []int64 `json:"ids"`
}

func (h *AccessHandler) Reorder(c *gin.Context) {
	var req reorderRequest
	if err := c.ShouldBindJSON(&req); err != nil || len(req.IDs) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "ids is required"})
		return
	}
	if err := h.access.ReorderRules(req.IDs); err != nil {
		respondAccessError(c, err)
		return
	}
	c.JSON(http.StatusOK, withReload(h.mgr, gin.H{"status": "reordered"}))
}

// --------------------------------------------------------------- policy

// Policy returns every http_access rule squid evaluates, in order, whoever
// wrote it (stock config, includes, or a panel feature).
func (h *AccessHandler) Policy(c *gin.Context) {
	content, err := h.mgr.ReadConfig()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	p := squid.ParsePolicy(content, h.env)
	c.JSON(http.StatusOK, gin.H{"rules": p.Rules, "acls": p.ACLs})
}

type testRequest struct {
	SrcIP     string `json:"src_ip"`
	URL       string `json:"url"`
	Method    string `json:"method"`
	User      string `json:"user"`
	UserAgent string `json:"user_agent"`
	// Time is "YYYY-MM-DDTHH:MM" in the server's local time; empty means now.
	Time string `json:"time"`
}

// Test evaluates a hypothetical request against the current policy.
func (h *AccessHandler) Test(c *gin.Context) {
	var req testRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
		return
	}

	r := squid.Request{
		SrcIP: strings.TrimSpace(req.SrcIP), URL: req.URL, Method: req.Method,
		User: strings.TrimSpace(req.User), UserAgent: req.UserAgent,
	}
	if req.Time != "" {
		t, err := time.ParseInLocation("2006-01-02T15:04", req.Time, time.Local)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "time must look like 2026-09-21T10:30"})
			return
		}
		r.Time = t
	}

	content, err := h.mgr.ReadConfig()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	p := squid.ParsePolicy(content, h.env)
	verdict, err := p.Evaluate(r, h.env)
	if err != nil {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, verdict)
}
