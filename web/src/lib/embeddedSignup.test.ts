import { describe, expect, it } from "vitest";
import { parseSignupMessage } from "./embeddedSignup";

const FB = "https://www.facebook.com";

describe("parseSignupMessage", () => {
  it("reads a standard finish event sent as a JSON string", () => {
    const msg = JSON.stringify({
      type: "WA_EMBEDDED_SIGNUP",
      event: "FINISH",
      data: { phone_number_id: "555", waba_id: "111", business_id: "777" },
    });
    expect(parseSignupMessage(FB, msg)).toEqual({ kind: "finish", wabaId: "111", phoneNumberId: "555", businessId: "777" });
  });

  it("accepts a coexistence finish with only the WABA", () => {
    const msg = { type: "WA_EMBEDDED_SIGNUP", event: "FINISH_WHATSAPP_BUSINESS_APP_ONBOARDING", data: { waba_id: "111" } };
    expect(parseSignupMessage(FB, msg)).toEqual({ kind: "finish", wabaId: "111", phoneNumberId: undefined, businessId: undefined });
  });

  it("reports where the user cancelled, and errors", () => {
    expect(parseSignupMessage(FB, { type: "WA_EMBEDDED_SIGNUP", event: "CANCEL", data: { current_step: "PHONE_NUMBER_SETUP" } }))
      .toEqual({ kind: "cancel", currentStep: "PHONE_NUMBER_SETUP" });
    expect(parseSignupMessage(FB, { type: "WA_EMBEDDED_SIGNUP", event: "CANCEL", data: { error_message: "boom" } }))
      .toEqual({ kind: "error", message: "boom" });
  });

  it("ignores other origins and unrelated messages", () => {
    const msg = { type: "WA_EMBEDDED_SIGNUP", event: "FINISH", data: { waba_id: "1" } };
    expect(parseSignupMessage("https://evil.example", msg)).toBeNull();
    expect(parseSignupMessage("https://evilfacebook.com", msg)).toBeNull();
    expect(parseSignupMessage("https://facebook.com.evil.example", msg)).toBeNull();
    expect(parseSignupMessage("http://www.facebook.com", msg)).toBeNull();
    expect(parseSignupMessage("https://business.facebook.com", msg)?.kind).toBe("finish");
    expect(parseSignupMessage(FB, "not json")).toBeNull();
    expect(parseSignupMessage(FB, { type: "OTHER" })).toBeNull();
  });
});
