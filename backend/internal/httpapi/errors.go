package httpapi

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/go-playground/validator/v10"

	"visionguard/backend/internal/domain"
)

// APIError is the uniform error body: {"error": {"code", "message", "details"}}.
type APIError struct {
	Status  int               `json:"-"`
	Code    string            `json:"code"`
	Message string            `json:"message"`
	Details map[string]string `json:"details,omitempty"`
}

func (e *APIError) Error() string { return e.Message }

func errBadRequest(msg string) *APIError {
	return &APIError{Status: http.StatusBadRequest, Code: "bad_request", Message: msg}
}

func errValidation(msg string, details map[string]string) *APIError {
	return &APIError{Status: http.StatusUnprocessableEntity, Code: "validation_error", Message: msg, Details: details}
}

func errUnauthorized(msg string) *APIError {
	return &APIError{Status: http.StatusUnauthorized, Code: "unauthorized", Message: msg}
}

func errForbidden() *APIError {
	return &APIError{Status: http.StatusForbidden, Code: "forbidden", Message: "insufficient role for this action"}
}

func errNotFound(what string) *APIError {
	return &APIError{Status: http.StatusNotFound, Code: "not_found", Message: what + " not found"}
}

func errConflict(msg string) *APIError {
	return &APIError{Status: http.StatusConflict, Code: "conflict", Message: msg}
}

// abort writes an error response, translating domain and binding errors.
func abort(c *gin.Context, err error) {
	var apiErr *APIError
	var verrs validator.ValidationErrors
	switch {
	case errors.As(err, &apiErr):
	case errors.Is(err, domain.ErrNotFound):
		apiErr = errNotFound("resource")
	case errors.Is(err, domain.ErrConflict):
		apiErr = errConflict("resource already exists")
	case errors.As(err, &verrs):
		details := map[string]string{}
		for _, fe := range verrs {
			details[strings.ToLower(fe.Field())] = describe(fe)
		}
		apiErr = errValidation("request validation failed", details)
	default:
		slog.Error("internal error", "err", err, "path", c.FullPath(), "request_id", c.GetString(requestIDKey))
		apiErr = &APIError{Status: http.StatusInternalServerError, Code: "internal", Message: "internal server error"}
	}
	c.AbortWithStatusJSON(apiErr.Status, gin.H{"error": apiErr})
}

func describe(fe validator.FieldError) string {
	switch fe.Tag() {
	case "required":
		return "is required"
	case "email":
		return "must be a valid email"
	case "min":
		return fmt.Sprintf("must be at least %s", fe.Param())
	case "max":
		return fmt.Sprintf("must be at most %s", fe.Param())
	case "oneof":
		return fmt.Sprintf("must be one of: %s", fe.Param())
	case "gt", "gte", "lte":
		return fmt.Sprintf("must be %s %s", fe.Tag(), fe.Param())
	}
	return "is invalid"
}

// bind decodes JSON and maps decoding problems to 400/422.
func bind(c *gin.Context, dst any) bool {
	if err := c.ShouldBindJSON(dst); err != nil {
		var verrs validator.ValidationErrors
		if errors.As(err, &verrs) {
			abort(c, err)
		} else {
			abort(c, errBadRequest("malformed JSON body: "+err.Error()))
		}
		return false
	}
	return true
}
