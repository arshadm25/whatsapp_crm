import { Component, type ReactNode } from "react";
import i18n from "../i18n";

// ErrorBoundary keeps a crash in one screen from blanking the whole dashboard: it shows the
// error and a reload button, and resets when the user navigates elsewhere (resetKey changes).
export default class ErrorBoundary extends Component<{ children: ReactNode; resetKey?: string }, { error: Error | null }> {
  state = { error: null as Error | null };

  static getDerivedStateFromError(error: Error) {
    return { error };
  }

  componentDidCatch(error: Error, info: { componentStack?: string | null }) {
    console.error("Screen crashed:", error, info.componentStack);
  }

  componentDidUpdate(prev: { resetKey?: string }) {
    if (this.state.error && prev.resetKey !== this.props.resetKey) this.setState({ error: null });
  }

  render() {
    if (!this.state.error) return this.props.children;
    return (
      <div className="card">
        <p>{i18n.t("common.crashed")}</p>
        <pre className="muted small" style={{ whiteSpace: "pre-wrap" }}>{this.state.error.message}</pre>
        <button className="primary" onClick={() => window.location.reload()}>{i18n.t("common.reload")}</button>
      </div>
    );
  }
}
