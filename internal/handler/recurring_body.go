package handler

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"reflect"
	"slices"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gin-gonic/gin/binding"
	"github.com/go-playground/validator/v10"
	"github.com/shopspring/decimal"

	"dashboardgo/internal/dto"
	"dashboardgo/internal/model"
	"dashboardgo/internal/recurrence"
)

// Upper bounds match the columns: quantity numeric(10,2), unit_price numeric(12,2).
var (
	maxQuantity  = decimal.New(1, 8)
	maxUnitPrice = decimal.New(1, 10)
)

const (
	dateLayout    = "2006-01-02"
	maxIntervalN  = 365
	maxPaymentDay = 365
)

func invalidBody(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalidBody, fmt.Sprintf(format, args...))
}

// maxBodyBytes caps a JSON request body.
const maxBodyBytes = 64 << 10

// limitedDecoder returns a JSON decoder over the request body, capped at maxBodyBytes.
func limitedDecoder(c *gin.Context) *json.Decoder {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxBodyBytes)
	return json.NewDecoder(c.Request.Body)
}

// decodeError turns a json decode error into a client message that never echoes the
// submitted value or names a Go type.
func decodeError(err error) error {
	var tooLarge *http.MaxBytesError
	var typeErr *json.UnmarshalTypeError
	switch {
	case errors.As(err, &tooLarge):
		return fmt.Errorf("decode body: %w", ErrBodyTooLarge)
	case errors.Is(err, io.EOF):
		return invalidBody("request body is required")
	case errors.As(err, &typeErr):
		field := typeErr.Field
		if field == "" {
			field = "request body"
		}
		return invalidBody("%s: must be %s", field, jsonTypeName(typeErr.Type))
	case strings.HasPrefix(err.Error(), "json: unknown field "):
		return invalidBody("%s", strings.TrimPrefix(err.Error(), "json: "))
	default:
		// *json.SyntaxError, io.ErrUnexpectedEOF and anything else the decoder reports.
		return invalidBody("malformed JSON")
	}
}

// jsonTypeName names the JSON type a Go type decodes from, with its article.
func jsonTypeName(t reflect.Type) string {
	for t != nil && t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if t == nil {
		return "a valid value"
	}
	switch t.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64,
		reflect.Float32, reflect.Float64:
		return "a number"
	case reflect.String:
		return "a string"
	case reflect.Bool:
		return "a boolean"
	case reflect.Slice, reflect.Array:
		return "an array"
	case reflect.Struct, reflect.Map:
		return "an object"
	default:
		return "a valid value"
	}
}

// expectNoTrailingData fails when anything but whitespace follows the JSON value.
func expectNoTrailingData(dec *json.Decoder) error {
	_, err := dec.Token()
	if errors.Is(err, io.EOF) {
		return nil
	}
	var tooLarge *http.MaxBytesError
	if errors.As(err, &tooLarge) {
		return fmt.Errorf("decode body: %w", ErrBodyTooLarge)
	}
	return invalidBody("unexpected data after the JSON body")
}

// decodeBody reads the JSON body into dst, rejecting unknown keys, then runs the binding tags.
func decodeBody(c *gin.Context, dst any) error {
	dec := limitedDecoder(c)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return decodeError(err)
	}
	if err := expectNoTrailingData(dec); err != nil {
		return err
	}
	if err := binding.Validator.ValidateStruct(dst); err != nil {
		var verrs validator.ValidationErrors
		if errors.As(err, &verrs) {
			return invalidBody("%s", describeValidation(dst, verrs))
		}
		return invalidBody("%s", err)
	}
	return nil
}

// describeValidation names each failing field by its JSON name.
func describeValidation(dst any, verrs validator.ValidationErrors) string {
	t := reflect.TypeOf(dst)
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	parts := make([]string, 0, len(verrs))
	for _, fe := range verrs {
		name := fe.Field()
		if sf, ok := t.FieldByName(fe.Field()); ok {
			if tag, _, _ := strings.Cut(sf.Tag.Get("json"), ","); tag != "" {
				name = tag
			}
		}
		parts = append(parts, fmt.Sprintf("%s: failed %q validation", name, fe.Tag()))
	}
	return strings.Join(parts, "; ")
}

// parseDecimal parses a decimal string with at most places digits after the point.
// "10.500" passes for 2 places because only the value counts.
func parseDecimal(field, s string, places int32) (decimal.Decimal, error) {
	d, err := decimal.NewFromString(s)
	if err != nil {
		return decimal.Decimal{}, invalidBody("%s: must be a decimal number", field)
	}
	if !d.Equal(d.Round(places)) {
		return decimal.Decimal{}, invalidBody("%s: at most %d decimal places", field, places)
	}
	return d, nil
}

func parseQuantity(s string) (decimal.Decimal, error) {
	d, err := parseDecimal("quantity", s, 2)
	if err != nil {
		return d, err
	}
	if !d.IsPositive() {
		return d, invalidBody("quantity: must be greater than 0")
	}
	if !d.LessThan(maxQuantity) {
		return d, invalidBody("quantity: must be less than %s", maxQuantity)
	}
	return d, nil
}

func parseUnitPrice(s string) (decimal.Decimal, error) {
	d, err := parseDecimal("unit_price", s, 2)
	if err != nil {
		return d, err
	}
	if d.IsNegative() {
		return d, invalidBody("unit_price: must not be negative")
	}
	if !d.LessThan(maxUnitPrice) {
		return d, invalidBody("unit_price: must be less than %s", maxUnitPrice)
	}
	return d, nil
}

func parseTaxRate(s string) (decimal.Decimal, error) {
	d, err := parseDecimal("tax_rate", s, 4)
	if err != nil {
		return d, err
	}
	if d.IsNegative() || d.GreaterThan(decimal.NewFromInt(1)) {
		return d, invalidBody("tax_rate: must be between 0 and 1")
	}
	return d, nil
}

func parseDate(field, s string, loc *time.Location) (time.Time, error) {
	t, err := time.ParseInLocation(dateLayout, s, loc)
	if err != nil {
		return time.Time{}, invalidBody("%s: must be a date as YYYY-MM-DD", field)
	}
	return t, nil
}

func parseUnit(s string) (recurrence.Unit, error) {
	switch u := recurrence.Unit(s); u {
	case recurrence.Day, recurrence.Week, recurrence.Month:
		return u, nil
	}
	return "", invalidBody("interval_unit: must be day, week or month")
}

func parseOptionalDate(field string, s *string, loc *time.Location) (*time.Time, error) {
	var out *time.Time
	if s != nil {
		t, err := parseDate(field, *s, loc)
		if err != nil {
			return nil, err
		}
		out = &t
	}
	return out, nil
}

// newPlanFromRequest converts a validated create body to the domain input.
func newPlanFromRequest(req dto.CreateRecurringPlanRequest, loc *time.Location) (model.NewRecurringPlan, error) {
	start, err := parseDate("start_date", req.StartDate, loc)
	if err != nil {
		return model.NewRecurringPlan{}, err
	}
	end, err := parseOptionalDate("end_date", req.EndDate, loc)
	if err != nil {
		return model.NewRecurringPlan{}, err
	}
	unit, err := parseUnit(req.IntervalUnit)
	if err != nil {
		return model.NewRecurringPlan{}, err
	}

	in := model.NewRecurringPlan{
		CustomerID: req.CustomerID,
		ServiceID:  req.ServiceID,
		LocationID: req.LocationID,
		StaffID:    req.StaffID,
		Rule:       recurrence.Rule{Unit: unit, Count: req.IntervalCount, Start: start, End: end},
		Quantity:   decimal.NewFromInt(1),
		TaxRate:    decimal.Zero,
	}
	if req.Quantity != nil {
		if in.Quantity, err = parseQuantity(*req.Quantity); err != nil {
			return model.NewRecurringPlan{}, err
		}
	}
	if req.UnitPrice != nil {
		price, err := parseUnitPrice(*req.UnitPrice)
		if err != nil {
			return model.NewRecurringPlan{}, err
		}
		in.UnitPrice = &price
	}
	if req.TaxRate != nil {
		if in.TaxRate, err = parseTaxRate(*req.TaxRate); err != nil {
			return model.NewRecurringPlan{}, err
		}
	}
	if req.PaymentTermsDays != nil {
		in.PaymentTermsDays = *req.PaymentTermsDays
	}
	return in, nil
}

func checkID(field string, id *int64) error {
	if id != nil && *id < 1 {
		return invalidBody("%s: must be at least 1", field)
	}
	return nil
}

// patchFromRequest converts a PATCH body to the domain patch, checking each Set field.
func patchFromRequest(req dto.UpdateRecurringPlanRequest, loc *time.Location) (model.RecurringPlanPatch, error) {
	var p model.RecurringPlanPatch
	var err error
	for _, n := range []struct {
		field     string
		set, null bool
	}{
		{"interval_unit", req.IntervalUnit.Set, req.IntervalUnit.Null},
		{"interval_count", req.IntervalCount.Set, req.IntervalCount.Null},
		{"start_date", req.StartDate.Set, req.StartDate.Null},
		{"quantity", req.Quantity.Set, req.Quantity.Null},
		{"unit_price", req.UnitPrice.Set, req.UnitPrice.Null},
		{"tax_rate", req.TaxRate.Set, req.TaxRate.Null},
		{"payment_terms_days", req.PaymentTermsDays.Set, req.PaymentTermsDays.Null},
	} {
		if err = rejectNull(n.field, n.set, n.null); err != nil {
			return p, err
		}
	}

	if req.LocationID.Set {
		if err = checkID("location_id", req.LocationID.Value); err != nil {
			return p, err
		}
		p.LocationID = model.Optional[*int64]{Set: true, Value: req.LocationID.Value}
	}
	if req.StaffID.Set {
		if err = checkID("staff_id", req.StaffID.Value); err != nil {
			return p, err
		}
		p.StaffID = model.Optional[*int64]{Set: true, Value: req.StaffID.Value}
	}
	if req.EndDate.Set {
		end, err := parseOptionalDate("end_date", req.EndDate.Value, loc)
		if err != nil {
			return p, err
		}
		p.EndDate = model.Optional[*time.Time]{Set: true, Value: end}
	}
	if req.IntervalUnit.Set {
		unit, err := parseUnit(req.IntervalUnit.Value)
		if err != nil {
			return p, err
		}
		p.IntervalUnit = model.Optional[recurrence.Unit]{Set: true, Value: unit}
	}
	if req.IntervalCount.Set {
		if req.IntervalCount.Value < 1 || req.IntervalCount.Value > maxIntervalN {
			return p, invalidBody("interval_count: must be between 1 and %d", maxIntervalN)
		}
		p.IntervalCount = model.Optional[int]{Set: true, Value: req.IntervalCount.Value}
	}
	if req.StartDate.Set {
		start, err := parseDate("start_date", req.StartDate.Value, loc)
		if err != nil {
			return p, err
		}
		p.StartDate = model.Optional[time.Time]{Set: true, Value: start}
	}
	if req.Quantity.Set {
		d, err := parseQuantity(req.Quantity.Value)
		if err != nil {
			return p, err
		}
		p.Quantity = model.Optional[decimal.Decimal]{Set: true, Value: d}
	}
	if req.UnitPrice.Set {
		d, err := parseUnitPrice(req.UnitPrice.Value)
		if err != nil {
			return p, err
		}
		p.UnitPrice = model.Optional[decimal.Decimal]{Set: true, Value: d}
	}
	if req.TaxRate.Set {
		d, err := parseTaxRate(req.TaxRate.Value)
		if err != nil {
			return p, err
		}
		p.TaxRate = model.Optional[decimal.Decimal]{Set: true, Value: d}
	}
	if req.PaymentTermsDays.Set {
		if req.PaymentTermsDays.Value < 0 || req.PaymentTermsDays.Value > maxPaymentDay {
			return p, invalidBody("payment_terms_days: must be between 0 and %d", maxPaymentDay)
		}
		p.PaymentTermsDays = model.Optional[int]{Set: true, Value: req.PaymentTermsDays.Value}
	}
	return p, nil
}

// decodePatch reads a PATCH body key by key so a bad value names its field, and unknown keys are rejected.
func decodePatch(c *gin.Context) (dto.UpdateRecurringPlanRequest, error) {
	var req dto.UpdateRecurringPlanRequest
	dec := limitedDecoder(c)
	var raw map[string]json.RawMessage
	if err := dec.Decode(&raw); err != nil {
		return req, decodeError(err)
	}
	if err := expectNoTrailingData(dec); err != nil {
		return req, err
	}
	fields := req.Fields()
	for _, key := range slices.Sorted(maps.Keys(raw)) {
		f, ok := fields[key]
		if !ok {
			return req, invalidBody("unknown field %q", key)
		}
		if err := f.UnmarshalJSON(raw[key]); err != nil {
			// Name the field and the expected type only, never the submitted value.
			var typeErr *json.UnmarshalTypeError
			if errors.As(err, &typeErr) {
				return req, invalidBody("%s: must be %s", key, jsonTypeName(typeErr.Type))
			}
			return req, invalidBody("%s: invalid value", key)
		}
	}
	return req, nil
}

// rejectNull fails when a non-nullable PATCH field was sent as JSON null.
func rejectNull(field string, set, null bool) error {
	if set && null {
		return invalidBody("%s: must not be null", field)
	}
	return nil
}
