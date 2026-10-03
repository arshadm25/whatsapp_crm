// Package metaevents receives and processes Meta's WhatsApp webhooks.
//
// The ingest deployment only verifies the X-Hub-Signature-256 header and enqueues the raw
// payload as one River job, so Meta always gets a fast 200. The worker routes each change to
// its tenant and applies it: inbound messages, delivery statuses, template review results,
// number limit changes, account removal, and the coexistence echoes, contact sync and chat
// history import.
package metaevents

import (
	"encoding/json"
	"strconv"
	"time"
)

// Payload is the envelope of every WhatsApp Business Account webhook.
type Payload struct {
	Object string  `json:"object"`
	Entry  []Entry `json:"entry"`
}

type Entry struct {
	ID      string   `json:"id"` // WABA ID
	Changes []Change `json:"changes"`
}

type Change struct {
	Field string          `json:"field"`
	Value json.RawMessage `json:"value"`
}

// MessagesValue is the value of "messages" and "smb_message_echoes" changes.
type MessagesValue struct {
	MessagingProduct string `json:"messaging_product"`
	Metadata         struct {
		DisplayPhoneNumber string `json:"display_phone_number"`
		PhoneNumberID      string `json:"phone_number_id"`
	} `json:"metadata"`
	Contacts []struct {
		WaID    string `json:"wa_id"`
		Profile struct {
			Name string `json:"name"`
		} `json:"profile"`
	} `json:"contacts"`
	Messages      []json.RawMessage `json:"messages"`
	MessageEchoes []json.RawMessage `json:"message_echoes"`
	Statuses      []Status          `json:"statuses"`
}

// MessageHeader holds the fields every message object shares; the full object is stored as content.
type MessageHeader struct {
	ID        string `json:"id"`
	From      string `json:"from"`
	To        string `json:"to"` // echoes only: the customer the business wrote to
	Timestamp string `json:"timestamp"`
	Type      string `json:"type"`
	Context   *struct {
		ID string `json:"id"`
	} `json:"context"`
	Text *struct {
		Body string `json:"body"`
	} `json:"text"`
	Image    *MediaRef `json:"image"`
	Video    *MediaRef `json:"video"`
	Audio    *MediaRef `json:"audio"`
	Document *MediaRef `json:"document"`
	Sticker  *MediaRef `json:"sticker"`
	Button   *struct {
		Text string `json:"text"`
	} `json:"button"`
	Reaction *struct {
		Emoji string `json:"emoji"`
	} `json:"reaction"`
	Interactive *struct {
		ButtonReply *struct {
			Title string `json:"title"`
		} `json:"button_reply"`
		ListReply *struct {
			Title string `json:"title"`
		} `json:"list_reply"`
		NfmReply *struct {
			Name         string `json:"name"`
			Body         string `json:"body"`
			ResponseJSON string `json:"response_json"`
		} `json:"nfm_reply"`
	} `json:"interactive"`
}

// MediaRef is the file part of an image, video, audio, document or sticker message.
type MediaRef struct {
	ID       string `json:"id"`
	Caption  string `json:"caption"`
	Filename string `json:"filename"`
}

type Status struct {
	ID           string `json:"id"` // wamid
	Status       string `json:"status"`
	Timestamp    string `json:"timestamp"`
	RecipientID  string `json:"recipient_id"`
	Conversation *struct {
		ID string `json:"id"`
	} `json:"conversation"`
	Pricing *struct {
		Billable *bool  `json:"billable"`
		Category string `json:"category"`
	} `json:"pricing"`
	Errors []struct {
		Code  int32  `json:"code"`
		Title string `json:"title"`
	} `json:"errors"`
}

// TemplateStatusValue is the value of "message_template_status_update".
type TemplateStatusValue struct {
	Event                   string          `json:"event"`
	MessageTemplateID       json.Number     `json:"message_template_id"`
	MessageTemplateName     string          `json:"message_template_name"`
	MessageTemplateLanguage string          `json:"message_template_language"`
	Reason                  *string         `json:"reason"`
	OtherInfo               json.RawMessage `json:"other_info"`
}

// QualityValue is the value of "phone_number_quality_update".
type QualityValue struct {
	DisplayPhoneNumber string `json:"display_phone_number"`
	Event              string `json:"event"`
	CurrentLimit       string `json:"current_limit"`
}

// NameUpdateValue is the value of "phone_number_name_update".
type NameUpdateValue struct {
	DisplayPhoneNumber    string  `json:"display_phone_number"`
	Decision              string  `json:"decision"` // APPROVED, REJECTED, DEFERRED
	RequestedVerifiedName string  `json:"requested_verified_name"`
	RejectionReason       *string `json:"rejection_reason"`
}

// TemplateQualityValue is the value of "message_template_quality_update".
type TemplateQualityValue struct {
	PreviousQualityScore string      `json:"previous_quality_score"`
	NewQualityScore      string      `json:"new_quality_score"`
	MessageTemplateID    json.Number `json:"message_template_id"`
}

// CapabilityValue is the value of "business_capability_update".
type CapabilityValue struct {
	MaxDailyConversationPerPhone int `json:"max_daily_conversation_per_phone"`
	MaxPhoneNumbersPerBusiness   int `json:"max_phone_numbers_per_business"`
}

// SecurityValue is the value of "security" (two-step verification PIN changes).
type SecurityValue struct {
	DisplayPhoneNumber string `json:"display_phone_number"`
	Event              string `json:"event"` // PIN_CHANGED, PIN_RESET_REQUEST
	Requester          string `json:"requester"`
}

// AccountUpdateValue is the value of "account_update".
type AccountUpdateValue struct {
	Event string `json:"event"`
}

// StateSyncValue is the value of "smb_app_state_sync" (coexistence contact changes).
type StateSyncValue struct {
	Metadata struct {
		PhoneNumberID string `json:"phone_number_id"`
	} `json:"metadata"`
	StateSync []struct {
		Type    string `json:"type"`   // contact
		Action  string `json:"action"` // add, edit, remove
		Contact struct {
			FullName    string `json:"full_name"`
			FirstName   string `json:"first_name"`
			PhoneNumber string `json:"phone_number"`
		} `json:"contact"`
	} `json:"state_sync"`
}

// HistoryValue is the value of "history": chat history a coexistence number's WhatsApp Business
// app shares after onboarding, sent in chunks. Each thread is one customer's chat; its messages
// are the customer's and the business's, oldest first. A business that declines to share history
// gets one entry with errors and no threads.
type HistoryValue struct {
	Metadata struct {
		PhoneNumberID string `json:"phone_number_id"`
	} `json:"metadata"`
	History []struct {
		Metadata struct {
			Phase      int `json:"phase"`
			ChunkOrder int `json:"chunk_order"`
			Progress   int `json:"progress"`
		} `json:"metadata"`
		Threads []struct {
			ID       string            `json:"id"` // the customer's WhatsApp ID
			Messages []json.RawMessage `json:"messages"`
		} `json:"threads"`
		Errors []struct {
			Code    int    `json:"code"`
			Title   string `json:"title"`
			Message string `json:"message"`
		} `json:"errors"`
	} `json:"history"`
}

// HistoryContext is the "history_context" of a history message: its delivery status at sync time.
type HistoryContext struct {
	HistoryContext *struct {
		Status string `json:"status"` // PENDING, SENT, DELIVERED, READ, PLAYED, ERROR
	} `json:"history_context"`
}

// unixTime parses Meta's string Unix timestamps; it returns fallback when the value is missing.
func unixTime(s string, fallback time.Time) time.Time {
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil || n <= 0 {
		return fallback
	}
	return time.Unix(n, 0).UTC()
}
