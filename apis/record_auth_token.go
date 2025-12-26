package apis

import (
	"database/sql"
	"errors"
	"net/http"

	"github.com/bosbase/bosbase-enterprise/core"
	validation "github.com/go-ozzo/ozzo-validation/v4"
	"github.com/go-ozzo/ozzo-validation/v4/is"
)

func recordBindToken(e *core.RequestEvent) error {
	collection, err := findAuthCollection(e)
	if err != nil {
		return err
	}

	if !collection.PasswordAuth.Enabled {
		return e.ForbiddenError("The collection is not configured to allow password authentication.", nil)
	}

	form := new(customTokenBindingForm)
	if err = e.BindBody(form); err != nil {
		return firstApiError(err, e.BadRequestError("An error occurred while loading the submitted data.", err))
	}
	if err = form.validate(); err != nil {
		return firstApiError(err, e.BadRequestError("An error occurred while validating the submitted data.", err))
	}

	record, err := e.App.FindAuthRecordByEmail(collection, form.Email)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return e.BadRequestError("Failed to bind token.", errors.New("invalid login credentials"))
		}
		return e.InternalServerError("", err)
	}

	if !record.ValidatePassword(form.Password) {
		return e.BadRequestError("Failed to bind token.", errors.New("invalid login credentials"))
	}

	event := new(core.RecordBindTokenRequestEvent)
	event.RequestEvent = e
	event.Collection = collection
	event.Record = record
	event.Token = form.Token
	event.Email = form.Email
	event.Password = form.Password

	return e.App.OnRecordBindTokenRequest().Trigger(event, func(e *core.RecordBindTokenRequestEvent) error {
		if err := e.App.BindCustomToken(e.Record, e.Token); err != nil {
			return firstApiError(err, e.InternalServerError("Failed to bind token.", err))
		}

		return execAfterSuccessTx(true, e.App, func() error {
			return e.NoContent(http.StatusNoContent)
		})
	})
}

func recordUnbindToken(e *core.RequestEvent) error {
	collection, err := findAuthCollection(e)
	if err != nil {
		return err
	}

	if !collection.PasswordAuth.Enabled {
		return e.ForbiddenError("The collection is not configured to allow password authentication.", nil)
	}

	form := new(customTokenBindingForm)
	if err = e.BindBody(form); err != nil {
		return firstApiError(err, e.BadRequestError("An error occurred while loading the submitted data.", err))
	}
	if err = form.validate(); err != nil {
		return firstApiError(err, e.BadRequestError("An error occurred while validating the submitted data.", err))
	}

	record, err := e.App.FindAuthRecordByEmail(collection, form.Email)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return e.BadRequestError("Failed to unbind token.", errors.New("invalid login credentials"))
		}
		return e.InternalServerError("", err)
	}

	if !record.ValidatePassword(form.Password) {
		return e.BadRequestError("Failed to unbind token.", errors.New("invalid login credentials"))
	}

	event := new(core.RecordUnbindTokenRequestEvent)
	event.RequestEvent = e
	event.Collection = collection
	event.Record = record
	event.Token = form.Token
	event.Email = form.Email
	event.Password = form.Password

	return e.App.OnRecordUnbindTokenRequest().Trigger(event, func(e *core.RecordUnbindTokenRequestEvent) error {
		if err := e.App.UnbindCustomToken(e.Record, e.Token); err != nil {
			return firstApiError(err, e.InternalServerError("Failed to unbind token.", err))
		}

		return execAfterSuccessTx(true, e.App, func() error {
			return e.NoContent(http.StatusNoContent)
		})
	})
}

func recordAuthWithToken(e *core.RequestEvent) error {
	collection, err := findAuthCollection(e)
	if err != nil {
		return err
	}

	form := new(authWithTokenForm)
	if err = e.BindBody(form); err != nil {
		return firstApiError(err, e.BadRequestError("An error occurred while loading the submitted data.", err))
	}
	if err = form.validate(); err != nil {
		return firstApiError(err, e.BadRequestError("An error occurred while validating the submitted data.", err))
	}

	e.Set(core.RequestEventKeyInfoContext, core.RequestInfoContextTokenAuth)

	event := new(core.RecordAuthWithTokenRequestEvent)
	event.RequestEvent = e
	event.Collection = collection
	event.Token = form.Token

	event.Record, err = e.App.FindAuthRecordByCustomToken(collection, form.Token)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return e.BadRequestError("Failed to authenticate.", errors.New("invalid token"))
		}
		return firstApiError(err, e.InternalServerError("Failed to authenticate.", err))
	}

	return e.App.OnRecordAuthWithTokenRequest().Trigger(event, func(e *core.RecordAuthWithTokenRequestEvent) error {
		return RecordAuthResponse(e.RequestEvent, e.Record, core.MFAMethodToken, nil)
	})
}

// -------------------------------------------------------------------

type customTokenBindingForm struct {
	Email    string `form:"email" json:"email"`
	Password string `form:"password" json:"password"`
	Token    string `form:"token" json:"token"`
}

func (form *customTokenBindingForm) validate() error {
	return validation.ValidateStruct(form,
		validation.Field(&form.Email, validation.Required, validation.Length(1, 255), is.EmailFormat),
		validation.Field(&form.Password, validation.Required, validation.Length(1, 255)),
		validation.Field(&form.Token, validation.Required, validation.Length(1, 1024)),
	)
}

type authWithTokenForm struct {
	Token string `form:"token" json:"token"`
}

func (form *authWithTokenForm) validate() error {
	return validation.ValidateStruct(form,
		validation.Field(&form.Token, validation.Required, validation.Length(1, 1024)),
	)
}
