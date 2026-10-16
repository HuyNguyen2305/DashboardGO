package handler

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	"dashboardgo/internal/daterange"
	"dashboardgo/internal/dto"
	"dashboardgo/internal/model"
)

const maxPlanListLimit = 200

// RecurringService is what the recurring plan handlers need from the service layer.
type RecurringService interface {
	Create(ctx context.Context, in model.NewRecurringPlan) (model.RecurringPlan, error)
	Get(ctx context.Context, id int64) (model.RecurringPlan, error)
	List(ctx context.Context, q model.PlanListQuery) (model.PlanPage, error)
	Update(ctx context.Context, id int64, patch model.RecurringPlanPatch) (model.RecurringPlan, error)
	Transition(ctx context.Context, id int64, action model.PlanAction) (model.RecurringPlan, error)
	Occurrences(ctx context.Context, id int64, w model.Window) ([]model.PlanOccurrence, error)
	ProjectedInvoices(ctx context.Context, id int64, w model.Window) ([]model.ProjectedInvoice, error)
	ProjectedEstimate(ctx context.Context, id int64, w model.Window) (model.ProjectedEstimate, error)
	CustomerOccurrences(ctx context.Context, customerID int64, w model.Window) ([]model.PlanOccurrence, error)
}

// RecurringHandler serves the recurring plan endpoints.
type RecurringHandler struct {
	svc   RecurringService
	dates *daterange.Resolver
}

// NewRecurringHandler builds a RecurringHandler.
func NewRecurringHandler(svc RecurringService, dates *daterange.Resolver) *RecurringHandler {
	return &RecurringHandler{svc: svc, dates: dates}
}

func (h *RecurringHandler) loc() *time.Location { return h.dates.Today().Location() }

// Create serves POST /api/v1/recurring-plans.
func (h *RecurringHandler) Create(c *gin.Context) {
	var req dto.CreateRecurringPlanRequest
	if err := decodeBody(c, &req); err != nil {
		WriteError(c, err)
		return
	}
	in, err := newPlanFromRequest(req, h.loc())
	if err != nil {
		WriteError(c, err)
		return
	}
	plan, err := h.svc.Create(c.Request.Context(), in)
	if err != nil {
		WriteError(c, err)
		return
	}
	c.JSON(http.StatusCreated, dto.NewRecurringPlanResponse(plan))
}

// Get serves GET /api/v1/recurring-plans/:id.
func (h *RecurringHandler) Get(c *gin.Context) {
	id, err := planID(c)
	if err != nil {
		WriteError(c, err)
		return
	}
	plan, err := h.svc.Get(c.Request.Context(), id)
	if err != nil {
		WriteError(c, err)
		return
	}
	c.JSON(http.StatusOK, dto.NewRecurringPlanResponse(plan))
}

// List serves GET /api/v1/recurring-plans?customer_id=&status=&limit=&after=.
func (h *RecurringHandler) List(c *gin.Context) {
	q, err := parsePlanListQuery(c)
	if err != nil {
		WriteError(c, err)
		return
	}
	page, err := h.svc.List(c.Request.Context(), q)
	if err != nil {
		WriteError(c, err)
		return
	}
	c.JSON(http.StatusOK, dto.NewRecurringPlanPage(page))
}

// Update serves PATCH /api/v1/recurring-plans/:id.
func (h *RecurringHandler) Update(c *gin.Context) {
	id, err := planID(c)
	if err != nil {
		WriteError(c, err)
		return
	}
	req, err := decodePatch(c)
	if err != nil {
		WriteError(c, err)
		return
	}
	patch, err := patchFromRequest(req, h.loc())
	if err != nil {
		WriteError(c, err)
		return
	}
	plan, err := h.svc.Update(c.Request.Context(), id, patch)
	if err != nil {
		WriteError(c, err)
		return
	}
	c.JSON(http.StatusOK, dto.NewRecurringPlanResponse(plan))
}

// Pause serves POST /api/v1/recurring-plans/:id/pause.
func (h *RecurringHandler) Pause(c *gin.Context) { h.transition(c, model.PlanPause) }

// Resume serves POST /api/v1/recurring-plans/:id/resume.
func (h *RecurringHandler) Resume(c *gin.Context) { h.transition(c, model.PlanResume) }

// Cancel serves POST /api/v1/recurring-plans/:id/cancel.
func (h *RecurringHandler) Cancel(c *gin.Context) { h.transition(c, model.PlanCancel) }

func (h *RecurringHandler) transition(c *gin.Context, action model.PlanAction) {
	id, err := planID(c)
	if err != nil {
		WriteError(c, err)
		return
	}
	plan, err := h.svc.Transition(c.Request.Context(), id, action)
	if err != nil {
		WriteError(c, err)
		return
	}
	c.JSON(http.StatusOK, dto.NewRecurringPlanResponse(plan))
}

// planID reads the :id path parameter as a positive int64.
func planID(c *gin.Context) (int64, error) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id < 1 {
		return 0, fmt.Errorf("%w: invalid plan id", ErrInvalidQuery)
	}
	return id, nil
}

func parsePlanListQuery(c *gin.Context) (model.PlanListQuery, error) {
	var q model.PlanListQuery
	if raw, ok := c.GetQuery("customer_id"); ok {
		id, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || id < 1 {
			return q, fmt.Errorf("%w: customer_id must be a positive whole number", ErrInvalidQuery)
		}
		q.CustomerID = &id
	}
	if status, ok := c.GetQuery("status"); ok {
		switch status {
		case model.PlanStatusActive, model.PlanStatusPaused, model.PlanStatusCanceled:
			q.Status = &status
		default:
			return q, fmt.Errorf("%w: status must be active, paused or canceled", ErrInvalidQuery)
		}
	}
	if raw, ok := c.GetQuery("limit"); ok {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 || n > maxPlanListLimit {
			return q, fmt.Errorf("%w: limit must be a whole number from 1 to %d", ErrInvalidQuery, maxPlanListLimit)
		}
		q.Limit = n
	}
	if raw, ok := c.GetQuery("after"); ok {
		n, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || n < 0 {
			return q, fmt.Errorf("%w: after must be a non-negative whole number", ErrInvalidQuery)
		}
		q.After = n
	}
	return q, nil
}

const (
	defaultListMonths     = 3
	defaultEstimateMonths = 12
)

// Occurrences serves GET /api/v1/recurring-plans/:id/occurrences?from=&to=.
func (h *RecurringHandler) Occurrences(c *gin.Context) {
	id, err := planID(c)
	if err != nil {
		WriteError(c, err)
		return
	}
	w, rng, err := h.parseWindow(c, defaultListMonths)
	if err != nil {
		WriteError(c, err)
		return
	}
	occs, err := h.svc.Occurrences(c.Request.Context(), id, w)
	if err != nil {
		WriteError(c, err)
		return
	}
	c.JSON(http.StatusOK, dto.NewOccurrenceList(rng, occs))
}

// ProjectedInvoices serves GET /api/v1/recurring-plans/:id/projected-invoices?from=&to=.
func (h *RecurringHandler) ProjectedInvoices(c *gin.Context) {
	id, err := planID(c)
	if err != nil {
		WriteError(c, err)
		return
	}
	w, rng, err := h.parseWindow(c, defaultListMonths)
	if err != nil {
		WriteError(c, err)
		return
	}
	invs, err := h.svc.ProjectedInvoices(c.Request.Context(), id, w)
	if err != nil {
		WriteError(c, err)
		return
	}
	c.JSON(http.StatusOK, dto.NewProjectedInvoiceList(rng, invs))
}

// ProjectedEstimate serves GET /api/v1/recurring-plans/:id/projected-estimate?from=&to=.
func (h *RecurringHandler) ProjectedEstimate(c *gin.Context) {
	id, err := planID(c)
	if err != nil {
		WriteError(c, err)
		return
	}
	// The response echoes the period actually priced, which starts no earlier than today.
	w, _, err := h.parseWindow(c, defaultEstimateMonths)
	if err != nil {
		WriteError(c, err)
		return
	}
	est, err := h.svc.ProjectedEstimate(c.Request.Context(), id, w)
	if err != nil {
		WriteError(c, err)
		return
	}
	c.JSON(http.StatusOK, dto.NewProjectedEstimateResponse(est))
}

// CustomerOccurrences serves GET /api/v1/customers/:id/recurring-occurrences?from=&to=.
func (h *RecurringHandler) CustomerOccurrences(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id < 1 {
		WriteError(c, fmt.Errorf("%w: invalid customer id", ErrInvalidQuery))
		return
	}
	w, rng, err := h.parseWindow(c, defaultListMonths)
	if err != nil {
		WriteError(c, err)
		return
	}
	occs, err := h.svc.CustomerOccurrences(c.Request.Context(), id, w)
	if err != nil {
		WriteError(c, err)
		return
	}
	c.JSON(http.StatusOK, dto.NewOccurrenceList(rng, occs))
}

// parseWindow reads from/to (YYYY-MM-DD, to is the last included day) in the business timezone.
// It returns the half-open Window for the service and the inclusive range to echo back.
func (h *RecurringHandler) parseWindow(c *gin.Context, defaultMonths int) (model.Window, dto.DateRange, error) {
	loc := h.loc()
	from := h.dates.Today()
	if raw, ok := c.GetQuery("from"); ok {
		t, err := time.ParseInLocation(dateLayout, raw, loc)
		if err != nil {
			return model.Window{}, dto.DateRange{}, fmt.Errorf("%w: from must be YYYY-MM-DD", ErrInvalidQuery)
		}
		from = t
	}
	// The default inclusive end is the day before from + n months, so the window is exactly n months.
	to := from.AddDate(0, defaultMonths, -1)
	if raw, ok := c.GetQuery("to"); ok {
		t, err := time.ParseInLocation(dateLayout, raw, loc)
		if err != nil {
			return model.Window{}, dto.DateRange{}, fmt.Errorf("%w: to must be YYYY-MM-DD", ErrInvalidQuery)
		}
		to = t
	}
	if to.Before(from) {
		return model.Window{}, dto.DateRange{}, fmt.Errorf("%w: to must not be before from", ErrInvalidQuery)
	}
	y, m, d := to.Date()
	w := model.Window{From: from, To: time.Date(y, m, d+1, 0, 0, 0, 0, loc)}
	return w, dto.CustomRange(from, to), nil
}
