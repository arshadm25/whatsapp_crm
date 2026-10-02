// Razorpay Checkout for our subscriptions. It opens as a pop-up over the dashboard, so it does
// not depend on Razorpay's hosted payment page. The webhook, not this callback, activates the plan.

/* eslint-disable @typescript-eslint/no-explicit-any */
declare global {
  interface Window {
    Razorpay?: any;
  }
}

export interface CheckoutReply {
  payment_url?: string;
  razorpay_key_id?: string;
  razorpay_subscription_id?: string;
}

const SCRIPT = "https://checkout.razorpay.com/v1/checkout.js";

function loadScript(): Promise<void> {
  if (window.Razorpay) return Promise.resolve();
  return new Promise((resolve, reject) => {
    const el = document.createElement("script");
    el.src = SCRIPT;
    el.onload = () => resolve();
    el.onerror = () => reject(new Error("Razorpay checkout could not be loaded"));
    document.head.appendChild(el);
  });
}

// canPopUp reports whether the reply carries what the pop-up needs.
export function canPopUp(r: CheckoutReply): boolean {
  return !!r.razorpay_key_id && !!r.razorpay_subscription_id;
}

// openCheckout shows the pop-up and calls onPaid after Razorpay confirms the first payment.
export async function openCheckout(r: CheckoutReply, name: string, onPaid: () => void): Promise<void> {
  await loadScript();
  const rz = new window.Razorpay({
    key: r.razorpay_key_id,
    subscription_id: r.razorpay_subscription_id,
    name,
    handler: () => onPaid(),
  });
  rz.open();
}
