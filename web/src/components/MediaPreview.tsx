import { useTranslation } from "react-i18next";
import { useMedia } from "../api/hooks";
import type { Message } from "../api/types";
import { formatSize } from "../lib/media";

// The file of an image, video, audio, document or sticker message, inline in the thread.
export default function MediaPreview({ message }: { message: Message }) {
  const { t } = useTranslation();
  const media = useMedia(message.media_id);
  const part = (message.content[message.type] ?? {}) as { link?: string; filename?: string };

  // Sent by link: nothing is stored, show the link.
  if (!message.media_id) {
    if (part.link) {
      return (
        <a className="media-file" href={part.link} target="_blank" rel="noreferrer">
          {part.filename || part.link}
        </a>
      );
    }
    const missing = message.direction === "outbound" || Date.now() - new Date(message.created_at).getTime() > 10 * 60_000;
    return <div className="media-pending muted small">{t(missing ? "inbox.attachmentMissing" : "inbox.attachmentLoading")}</div>;
  }
  if (!media.data) {
    return <div className="media-pending muted small">{media.isError ? t("inbox.attachmentMissing") : "…"}</div>;
  }
  const { url, filename, size_bytes } = media.data;
  switch (message.type) {
    case "image":
    case "sticker":
      return (
        <a href={url} target="_blank" rel="noreferrer">
          <img className={`media-${message.type}`} src={url} alt={filename ?? message.type} loading="lazy" />
        </a>
      );
    case "video":
      return <video className="media-video" src={url} controls preload="metadata" />;
    case "audio":
      return <audio className="media-audio" src={url} controls preload="metadata" />;
    default:
      return (
        <a className="media-file" href={url} download={filename ?? undefined}>
          📄 {filename || t("inbox.download")} <span className="muted">· {formatSize(size_bytes)}</span>
        </a>
      );
  }
}
