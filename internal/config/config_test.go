package config_test

import (
	"strings"
	"testing"

	"github.com/arshadm25/whatsapp_crm/internal/config"
)

func TestMicrosoft365MailSettings(t *testing.T) {
	t.Setenv("ECOGO_MAIL_PROVIDER", "microsoft365")
	t.Setenv("ECOGO_M365_TENANT_ID", "tenant")
	c, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	// The webhook receiver sends no mail, so it starts without the app's secret.
	if err := c.Require(); err != nil {
		t.Errorf("Require() = %v", err)
	}
	err = c.Require("ECOGO_MAIL")
	if err == nil || !strings.Contains(err.Error(), "ECOGO_M365_CLIENT_ID") || !strings.Contains(err.Error(), "ECOGO_M365_CLIENT_SECRET") ||
		strings.Contains(err.Error(), "ECOGO_M365_TENANT_ID") {
		t.Errorf("Require(ECOGO_MAIL) = %v", err)
	}

	t.Setenv("ECOGO_MAIL_PROVIDER", "sendgrid")
	if _, err := config.Load(); err == nil {
		t.Error("an unknown mail provider was accepted")
	}
}
