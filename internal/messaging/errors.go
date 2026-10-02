package messaging

import (
	"net/http"

	"github.com/arshadm25/whatsapp_crm/internal/httpx"
)

// metaErrors maps Meta's send and delivery error codes to the stable codes of the public API
// (api/openapi.yaml, Error) and a message a business can act on. Codes not listed become
// meta_error with Meta's own title.
var metaErrors = map[int32]struct{ code, message string }{
	190:    {"number_not_connected", "Meta no longer accepts this account's access. Reconnect WhatsApp from Numbers."},
	368:    {"meta_error", "Meta has temporarily blocked this account for policy violations."},
	130429: {"rate_limited", "Meta's sending rate limit for this number was reached."},
	130472: {"meta_error", "Meta did not deliver this message as part of an experiment on the customer's account."},
	131026: {"recipient_not_on_whatsapp", "The message could not be delivered. The number may not be on WhatsApp, or the customer's app is out of date."},
	131031: {"number_not_connected", "Meta has locked this WhatsApp account. Check WhatsApp Manager."},
	131047: {"window_closed", "More than 24 hours have passed since the customer last wrote. Send an approved template instead."},
	131048: {"messaging_limit_reached", "Meta limited sending from this number because of customer feedback. Try again later."},
	131049: {"meta_error", "Meta chose not to deliver this marketing message to keep engagement healthy. Try again later."},
	131050: {"contact_opted_out", "The customer has stopped marketing messages from this business."},
	131051: {"invalid_request", "Meta does not support this message type."},
	131052: {"meta_error", "Meta could not download the media from the link."},
	131053: {"meta_error", "Meta could not use this media. Check its type and size."},
	131056: {"rate_limited", "Too many messages to this customer in a short time. Wait a little and try again."},
	132000: {"template_param_mismatch", "The number of variables does not match the template."},
	132001: {"template_not_approved", "Meta has no approved template with this name and language."},
	132005: {"template_param_mismatch", "The template text is too long once the variables are filled in."},
	132007: {"template_param_mismatch", "The variables break Meta's content rules for templates."},
	132012: {"template_param_mismatch", "A variable has the wrong format for this template."},
	132015: {"template_not_approved", "This template is paused because of low quality."},
	132016: {"template_not_approved", "This template is disabled because of low quality."},
	133010: {"number_not_connected", "This number is not registered with the WhatsApp Cloud API."},
}

// Failures that happen on our side are stored with negative error codes so they never clash
// with Meta's.
const (
	codeNotConnected int32 = -1
	codeUnreachable  int32 = -2
)

var ownErrors = map[int32]struct{ code, message string }{
	codeNotConnected: {"number_not_connected", "This number is not connected. Reconnect it from Numbers."},
	codeUnreachable:  {"meta_error", "We could not reach Meta to send this message."},
}

// ErrorView is a failed message's error, in the API error shape.
type ErrorView struct {
	Code          string `json:"code"`
	Message       string `json:"message"`
	MetaErrorCode *int32 `json:"meta_error_code,omitempty"`
}

func errorView(code *int32, title *string) *ErrorView {
	if code == nil && title == nil {
		return nil
	}
	v := &ErrorView{Code: "meta_error", Message: "Meta did not deliver this message."}
	if title != nil {
		v.Message = *title
	}
	if code == nil {
		return v
	}
	if m, ok := ownErrors[*code]; ok {
		v.Code, v.Message = m.code, m.message
		return v
	}
	v.MetaErrorCode = code
	if m, ok := metaErrors[*code]; ok {
		v.Code, v.Message = m.code, m.message
	}
	return v
}

func unprocessable(code, param, message string) error {
	return &httpx.Error{Status: http.StatusUnprocessableEntity, Code: code, Param: param, Message: message}
}
