import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import "../i18n";
import { ReadMessages } from "./Admin";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const conversation = { id: "c1", phone_number_id: "p1", contact_wa_id: "910000000001", status: "open", last_message_at: null };

describe("admin ReadMessages", () => {
  let host: HTMLDivElement;
  let root: Root;
  const fetchMock = vi.fn();

  beforeEach(() => {
    document.cookie = "ecogo_csrf=tok";
    fetchMock.mockReset();
    vi.stubGlobal("fetch", fetchMock);
    host = document.createElement("div");
    document.body.appendChild(host);
    root = createRoot(host);
  });
  afterEach(() => {
    act(() => root.unmount());
    host.remove();
    vi.unstubAllGlobals();
  });

  it("opens as a modal dialog asking for a reason, then posts it and shows the messages", async () => {
    const onClose = vi.fn();
    act(() => root.render(<ReadMessages tenantId="t1" conversation={conversation} onClose={onClose} />));

    // A fixed overlay, so it is on screen wherever the row was in the list.
    const dialog = host.querySelector('.modal-backdrop [role="dialog"]');
    expect(dialog).not.toBeNull();
    expect(dialog!.getAttribute("aria-modal")).toBe("true");
    const input = dialog!.querySelector("input")!;
    expect(input.required).toBe(true);
    expect(input.minLength).toBe(5);
    expect(document.activeElement).toBe(input);

    fetchMock.mockResolvedValue(new Response(JSON.stringify({ data: [
      { id: "m1", direction: "inbound", type: "text", content: { text: { body: "hello there" } }, status: "delivered", created_at: "2026-10-30T10:00:00Z" },
    ] }), { status: 200 }));
    await act(async () => {
      const set = Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, "value")!.set!;
      set.call(input, "Support ticket 123");
      input.dispatchEvent(new Event("input", { bubbles: true }));
    });
    await act(async () => {
      dialog!.querySelector("form")!.dispatchEvent(new Event("submit", { bubbles: true, cancelable: true }));
    });

    expect(fetchMock).toHaveBeenCalledTimes(1);
    const [url, init] = fetchMock.mock.calls[0];
    expect(url).toBe("/internal/admin/tenants/t1/conversations/c1/messages");
    expect(init.method).toBe("POST");
    expect(JSON.parse(init.body)).toEqual({ reason: "Support ticket 123" });
    expect(host.querySelector(".admin-thread")!.textContent).toContain("hello there");

    act(() => { document.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape" })); });
    expect(onClose).toHaveBeenCalled();
  });
});
