package apis

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/bosbase/bosbase-enterprise/core"
	"github.com/bosbase/bosbase-enterprise/tools/router"
	validation "github.com/go-ozzo/ozzo-validation/v4"
	"github.com/go-ozzo/ozzo-validation/v4/is"
)

// bindActivationApi registers activation status and verification endpoints.
func bindActivationApi(app core.App, rg *router.RouterGroup[*core.RequestEvent]) {
	sub := rg.Group("/activation").Bind(RequireSuperuserAuth())
	sub.GET("/status", activationStatus)
	sub.POST("/verify", activationVerify)

	rg.POST("/activation/verify/public", activationVerifyPublic)
}

func activationStatus(e *core.RequestEvent) error {
	status := e.App.Settings().CurrentActivationStatus(time.Now())
	return e.JSON(http.StatusOK, status)
}

type activationVerifyForm struct {
	Email string `json:"email" form:"email"`
	Code  string `json:"code" form:"code"`
	Mode  string `json:"mode" form:"mode"`
}

func (form *activationVerifyForm) validate() error {
	return validation.ValidateStruct(form,
		validation.Field(&form.Email, validation.Required, is.Email),
		validation.Field(&form.Code, validation.Required),
		validation.Field(&form.Mode, validation.In("", "offline", "online")),
	)
}

func activationVerify(e *core.RequestEvent) error {
	form := &activationVerifyForm{}
	if err := e.BindBody(form); err != nil {
		return e.BadRequestError("An error occurred while loading the submitted data.", err)
	}

	return processActivationVerification(e, form, false)
}

// activationVerifyPublic allows activation without auth and returns a Markdown summary.
func activationVerifyPublic(e *core.RequestEvent) error {
	form := &activationVerifyForm{}
	if err := e.BindBody(form); err != nil {
		return e.BadRequestError("An error occurred while loading the submitted data.", err)
	}

	return processActivationVerification(e, form, true)
}

func formatActivationMarkdown(status core.ActivationStatus) string {
	var builder strings.Builder

	builder.WriteString("## Activation Verified\n\n")
	builder.WriteString("| Field | Value |\n")
	builder.WriteString("| --- | --- |\n")
	builder.WriteString(fmt.Sprintf("| Email | %s |\n", status.ActivationEmail))

	mode := status.ActivationMode
	if mode == "" {
		mode = "offline"
	}
	builder.WriteString(fmt.Sprintf("| Mode | %s |\n", strings.ToUpper(mode)))

	if status.SubscriptionExpires.IsZero() {
		builder.WriteString("| Expires | - |\n")
	} else {
		builder.WriteString(fmt.Sprintf("| Expires | %s |\n", status.SubscriptionExpires.String()))
	}

	message := status.Message
	if message == "" {
		message = "Activation completed"
	}
	builder.WriteString(fmt.Sprintf("| Status | %s |\n", message))

	builder.WriteString("\n> Save this document as proof of activation.\n")

	return builder.String()
}

func writeMarkdown(e *core.RequestEvent, status int, body string) error {
	e.Response.Header().Set("Content-Type", "text/markdown; charset=utf-8")
	e.Response.WriteHeader(status)
	_, err := e.Response.Write([]byte(body))
	return err
}

func processActivationVerification(e *core.RequestEvent, form *activationVerifyForm, respondMarkdown bool) error {
	if err := form.validate(); err != nil {
		return firstApiError(err, e.BadRequestError("An error occurred while validating the submitted data.", err))
	}

	settings := e.App.Settings()
	now := time.Now()
	currentStatus := settings.CurrentActivationStatus(now)

	result, err := core.VerifyActivationCode(e.App, form.Email, form.Code)
	if err != nil {
		// If already active, keep the status unchanged and return it.
		if currentStatus.Activated && !currentStatus.IsExpired {
			if respondMarkdown {
				return writeMarkdown(e, http.StatusOK, formatActivationMarkdown(currentStatus))
			}
			return e.JSON(http.StatusOK, currentStatus)
		}

		return e.BadRequestError("Invalid activation code.", err)
	}

	settings.ApplyActivationVerification(result)

	if err := e.App.Save(settings); err != nil {
		return e.InternalServerError("Failed to save activation data.", err)
	}

	newStatus := settings.CurrentActivationStatus(time.Now())

	if respondMarkdown {
		return writeMarkdown(e, http.StatusOK, formatActivationMarkdown(newStatus))
	}

	return e.JSON(http.StatusOK, newStatus)
}
