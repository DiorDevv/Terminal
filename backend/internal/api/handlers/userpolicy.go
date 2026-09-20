package handlers

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	"squidadmin/backend/internal/squid"
)

// UserPolicyHandler serves proxy account settings (disable, expiry, quota,
// notes), user groups, speed / download limits and CSV import / export.
type UserPolicyHandler struct {
	policy *squid.UserPolicy
	mgr    *squid.Manager
}

func NewUserPolicyHandler(policy *squid.UserPolicy, mgr *squid.Manager) *UserPolicyHandler {
	return &UserPolicyHandler{policy: policy, mgr: mgr}
}

// fail answers a rejected value with 422 and anything else with 500.
func (h *UserPolicyHandler) fail(c *gin.Context, err error) {
	var pe *squid.PolicyError
	if errors.As(err, &pe) {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"error": pe.Msg})
		return
	}
	c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
}

// reloaded adds the outcome of reloading squid to a response when squid.conf
// changed. The reload is verified and rolled back by the manager.
func (h *UserPolicyHandler) reloaded(resp gin.H, changed bool) gin.H {
	if !changed {
		return resp
	}
	if err := h.mgr.Reconfigure(); err != nil {
		resp["reloaded"] = false
		resp["reload_error"] = err.Error()
	} else {
		resp["reloaded"] = true
	}
	return resp
}

func (h *UserPolicyHandler) idParam(c *gin.Context) (int64, bool) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id < 1 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return 0, false
	}
	return id, true
}

// ---------------------------------------------------------------- accounts

func (h *UserPolicyHandler) Users(c *gin.Context) {
	users, err := h.policy.Users()
	if err != nil {
		h.fail(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"users": users})
}

func (h *UserPolicyHandler) UpdateUser(c *gin.Context) {
	var patch squid.UserPatch
	if err := c.ShouldBindJSON(&patch); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
		return
	}
	changed, err := h.policy.Update(c.Param("username"), patch)
	if err != nil {
		h.fail(c, err)
		return
	}
	c.JSON(http.StatusOK, h.reloaded(gin.H{"status": "saved"}, changed))
}

func (h *UserPolicyHandler) Export(c *gin.Context) {
	c.Header("Content-Type", "text/csv; charset=utf-8")
	c.Header("Content-Disposition", `attachment; filename="proxy-users-`+time.Now().Format("2006-01-02")+`.csv"`)
	if err := h.policy.ExportCSV(c.Writer); err != nil {
		// Headers are already sent when writing fails midway; log-worthy only.
		c.Error(err)
	}
}

// Import reads a CSV file from the request body. ?dry_run=1 only reports.
func (h *UserPolicyHandler) Import(c *gin.Context) {
	dry := c.Query("dry_run") == "1" || c.Query("dry_run") == "true"
	body := http.MaxBytesReader(c.Writer, c.Request.Body, 2<<20)
	defer body.Close()
	data, err := io.ReadAll(body)
	if err != nil {
		c.JSON(http.StatusRequestEntityTooLarge, gin.H{"error": "the file is too large"})
		return
	}
	report, changed, err := h.policy.ImportCSV(bytes.NewReader(data), dry)
	if err != nil {
		h.fail(c, err)
		return
	}
	resp := gin.H{"report": report}
	if report.Errors > 0 {
		// Nothing was applied; 422 lets the UI show the row errors as a failure.
		resp["error"] = "the file has errors; nothing was imported"
		c.JSON(http.StatusUnprocessableEntity, resp)
		return
	}
	c.JSON(http.StatusOK, h.reloaded(resp, changed))
}

// ------------------------------------------------------------- user groups

func (h *UserPolicyHandler) Groups(c *gin.Context) {
	groups, err := h.policy.Groups()
	if err != nil {
		h.fail(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"groups": groups})
}

func (h *UserPolicyHandler) CreateGroup(c *gin.Context) {
	var req struct {
		Name string `json:"name"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "name is required"})
		return
	}
	g, err := h.policy.CreateGroup(req.Name)
	if err != nil {
		h.fail(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"group": g})
}

func (h *UserPolicyHandler) DeleteGroup(c *gin.Context) {
	id, ok := h.idParam(c)
	if !ok {
		return
	}
	changed, err := h.policy.DeleteGroup(id)
	if err != nil {
		h.fail(c, err)
		return
	}
	c.JSON(http.StatusOK, h.reloaded(gin.H{"status": "deleted"}, changed))
}

func (h *UserPolicyHandler) SetMembers(c *gin.Context) {
	id, ok := h.idParam(c)
	if !ok {
		return
	}
	var req struct {
		Members []string `json:"members"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "members is required"})
		return
	}
	changed, err := h.policy.SetMembers(id, req.Members)
	if err != nil {
		h.fail(c, err)
		return
	}
	c.JSON(http.StatusOK, h.reloaded(gin.H{"status": "saved"}, changed))
}

// ------------------------------------------------------------------ limits

func (h *UserPolicyHandler) Limits(c *gin.Context) {
	limits, err := h.policy.Limits()
	if err != nil {
		h.fail(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"limits": limits})
}

func (h *UserPolicyHandler) CreateLimit(c *gin.Context) {
	var req squid.LimitRule
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
		return
	}
	rule, changed, err := h.policy.CreateLimit(req)
	if err != nil {
		h.fail(c, err)
		return
	}
	c.JSON(http.StatusOK, h.reloaded(gin.H{"limit": rule}, changed))
}

func (h *UserPolicyHandler) UpdateLimit(c *gin.Context) {
	id, ok := h.idParam(c)
	if !ok {
		return
	}
	var req squid.LimitRule
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
		return
	}
	req.ID = id
	rule, changed, err := h.policy.UpdateLimit(req)
	if err != nil {
		h.fail(c, err)
		return
	}
	c.JSON(http.StatusOK, h.reloaded(gin.H{"limit": rule}, changed))
}

func (h *UserPolicyHandler) DeleteLimit(c *gin.Context) {
	id, ok := h.idParam(c)
	if !ok {
		return
	}
	changed, err := h.policy.DeleteLimit(id)
	if err != nil {
		h.fail(c, err)
		return
	}
	c.JSON(http.StatusOK, h.reloaded(gin.H{"status": "deleted"}, changed))
}
