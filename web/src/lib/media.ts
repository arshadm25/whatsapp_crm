// WhatsApp's media rules, mirrored from the backend so a wrong file is caught before upload.

export type MediaKind = "image" | "video" | "audio" | "document" | "sticker";

const MB = 1024 * 1024;

const TYPES: Record<string, { kind: MediaKind; limit: number }> = {
  "image/jpeg": { kind: "image", limit: 5 * MB },
  "image/png": { kind: "image", limit: 5 * MB },
  "image/webp": { kind: "sticker", limit: 500 * 1024 },
  "video/mp4": { kind: "video", limit: 16 * MB },
  "video/3gpp": { kind: "video", limit: 16 * MB },
  "audio/aac": { kind: "audio", limit: 16 * MB },
  "audio/amr": { kind: "audio", limit: 16 * MB },
  "audio/mpeg": { kind: "audio", limit: 16 * MB },
  "audio/mp4": { kind: "audio", limit: 16 * MB },
  "audio/ogg": { kind: "audio", limit: 16 * MB },
  "text/plain": { kind: "document", limit: 100 * MB },
  "application/pdf": { kind: "document", limit: 100 * MB },
  "application/msword": { kind: "document", limit: 100 * MB },
  "application/vnd.ms-excel": { kind: "document", limit: 100 * MB },
  "application/vnd.ms-powerpoint": { kind: "document", limit: 100 * MB },
  "application/vnd.openxmlformats-officedocument.wordprocessingml.document": { kind: "document", limit: 100 * MB },
  "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet": { kind: "document", limit: 100 * MB },
  "application/vnd.openxmlformats-officedocument.presentationml.presentation": { kind: "document", limit: 100 * MB },
};

// The file picker's accept list.
export const ACCEPT = Object.keys(TYPES).join(",");

export const MEDIA_TYPES = new Set(["image", "video", "audio", "document", "sticker"]);

// How a file would be sent, or null when WhatsApp does not take its type.
export function mediaKind(mimeType: string): { kind: MediaKind; limit: number } | null {
  return TYPES[mimeType.split(";")[0].trim().toLowerCase()] ?? null;
}

// Audio and stickers cannot carry a caption.
export function takesCaption(kind: MediaKind): boolean {
  return kind !== "audio" && kind !== "sticker";
}

export function formatSize(bytes: number): string {
  if (bytes >= MB) return `${Math.round((bytes / MB) * 10) / 10} MB`;
  return `${Math.max(1, Math.round(bytes / 1024))} KB`;
}
