// Meta Embedded Signup v4. The Configuration ID decides the Embedded Signup version, so the
// popup is opened with extras.setup only; the coexistence path adds its featureType.
//
// The popup reports progress twice: a window "message" event of type WA_EMBEDDED_SIGNUP
// (with the WABA, phone number and business IDs, or where the user cancelled), and the
// FB.login callback with a short-lived authorization code. Both are needed.

import type { OnboardingFlow } from "../api/types";

/* eslint-disable @typescript-eslint/no-explicit-any */
declare global {
  interface Window {
    FB?: any;
    fbAsyncInit?: () => void;
  }
}

export type SignupEvent =
  | { kind: "finish"; wabaId: string; phoneNumberId?: string; businessId?: string }
  | { kind: "cancel"; currentStep?: string }
  | { kind: "error"; message: string };

const FACEBOOK_ORIGINS = ["https://www.facebook.com", "https://web.facebook.com"];

// parseSignupMessage reads one window message; it returns null for anything that is not ours.
export function parseSignupMessage(origin: string, raw: unknown): SignupEvent | null {
  if (!FACEBOOK_ORIGINS.includes(origin)) return null;
  let data: any = raw;
  if (typeof raw === "string") {
    try {
      data = JSON.parse(raw);
    } catch {
      return null;
    }
  }
  if (!data || data.type !== "WA_EMBEDDED_SIGNUP") return null;
  const d = data.data ?? {};
  switch (data.event) {
    case "FINISH":
    case "FINISH_ONLY_WABA":
    case "FINISH_WHATSAPP_BUSINESS_APP_ONBOARDING":
      if (!d.waba_id) return { kind: "error", message: "Meta did not return a WhatsApp Business Account." };
      return {
        kind: "finish",
        wabaId: String(d.waba_id),
        phoneNumberId: d.phone_number_id ? String(d.phone_number_id) : undefined,
        businessId: d.business_id ? String(d.business_id) : undefined,
      };
    case "CANCEL":
      if (d.error_message) return { kind: "error", message: String(d.error_message) };
      return { kind: "cancel", currentStep: d.current_step ? String(d.current_step) : undefined };
    case "ERROR":
      return { kind: "error", message: String(d.error_message ?? "Unknown error") };
    default:
      return null;
  }
}

let sdkPromise: Promise<void> | null = null;

export function loadFacebookSdk(appId: string, version: string): Promise<void> {
  if (sdkPromise) return sdkPromise;
  sdkPromise = new Promise((resolve, reject) => {
    window.fbAsyncInit = () => {
      window.FB.init({ appId, autoLogAppEvents: true, xfbml: false, version });
      resolve();
    };
    const s = document.createElement("script");
    s.src = "https://connect.facebook.net/en_US/sdk.js";
    s.async = true;
    s.defer = true;
    s.crossOrigin = "anonymous";
    s.onerror = () => {
      sdkPromise = null;
      reject(new Error("Could not load Meta's sign-up script. Check your connection or ad blocker."));
    };
    document.body.appendChild(s);
  });
  return sdkPromise;
}

export interface SignupResult {
  code: string | null;
  event: SignupEvent | null;
}

// launchEmbeddedSignup opens the popup and resolves once both the login callback and the
// session event have arrived (or the popup closed without them).
export function launchEmbeddedSignup(configId: string, flow: OnboardingFlow): Promise<SignupResult> {
  return new Promise((resolve) => {
    let event: SignupEvent | null = null;
    let code: string | null | undefined;
    let settled = false;

    const finish = () => {
      if (settled || code === undefined) return;
      // The session event normally arrives before the login callback; allow it a moment.
      if (!event && code) {
        setTimeout(() => done(), 1500);
        return;
      }
      done();
    };
    const done = () => {
      if (settled) return;
      settled = true;
      window.removeEventListener("message", onMessage);
      resolve({ code: code ?? null, event });
    };
    const onMessage = (e: MessageEvent) => {
      const parsed = parseSignupMessage(e.origin, e.data);
      if (parsed) {
        event = parsed;
        finish();
      }
    };
    window.addEventListener("message", onMessage);

    const extras: Record<string, unknown> = { setup: {} };
    if (flow === "coexistence") {
      extras.featureType = "whatsapp_business_app_onboarding";
      extras.sessionInfoVersion = "3";
    }
    window.FB.login(
      (response: any) => {
        code = response?.authResponse?.code ?? null;
        finish();
      },
      {
        config_id: configId,
        response_type: "code",
        override_default_response_type: true,
        extras,
      },
    );
  });
}
