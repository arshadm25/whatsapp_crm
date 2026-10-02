import { describe, expect, it } from "vitest";
import { formatSize, mediaKind, takesCaption } from "./media";

describe("mediaKind", () => {
  it("maps WhatsApp's types and limits", () => {
    expect(mediaKind("image/png")).toEqual({ kind: "image", limit: 5 * 1024 * 1024 });
    expect(mediaKind("image/webp")?.kind).toBe("sticker");
    expect(mediaKind("audio/ogg; codecs=opus")?.kind).toBe("audio");
    expect(mediaKind("application/pdf")?.kind).toBe("document");
    expect(mediaKind("application/zip")).toBeNull();
  });
  it("knows which kinds take a caption", () => {
    expect(takesCaption("image")).toBe(true);
    expect(takesCaption("audio")).toBe(false);
  });
  it("formats sizes", () => {
    expect(formatSize(500 * 1024)).toBe("500 KB");
    expect(formatSize(16 * 1024 * 1024)).toBe("16 MB");
  });
});
