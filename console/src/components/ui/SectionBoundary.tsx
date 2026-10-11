import { Component, type ErrorInfo, type ReactNode } from "react";
import { usePreferences } from "../../lib/preferences";
import { ErrorBanner } from "./Feedback";

interface SectionBoundaryProps {
  children: ReactNode;
  /** Names the section in the fallback, e.g. "Recent activity". */
  title?: string;
  /** A change in any value (a route, a tab) clears a previous failure. */
  resetKeys?: readonly unknown[];
  /** Refreshed data clears a failure only after a pending Retry completes. */
  recoveryKeys?: readonly unknown[];
  /** Runs before the section re-renders after Retry, e.g. to refetch. */
  onReset?: () => void | Promise<unknown>;
}

interface SectionBoundaryState {
  error: unknown;
  failed: boolean;
}

function SectionFallback({
  error,
  title,
  onRetry,
}: {
  error: unknown;
  title?: string;
  onRetry: () => void;
}) {
  const { text } = usePreferences();
  return (
    <div className="ag-section-fallback">
      <ErrorBanner
        title={
          title ?? text("此区域暂时无法显示", "This section is unavailable")
        }
        error={error}
        onRetry={onRetry}
      />
    </div>
  );
}

function sameKeys(
  left: readonly unknown[] = [],
  right: readonly unknown[] = [],
) {
  return (
    left.length === right.length &&
    left.every((value, index) => Object.is(value, right[index]))
  );
}

/**
 * Contains a render failure to the section that caused it (issue 317), so one
 * bad answer or broken panel leaves the rest of the page usable instead of
 * replacing it with the route error page.
 */
export class SectionBoundary extends Component<
  SectionBoundaryProps,
  SectionBoundaryState
> {
  state: SectionBoundaryState = { error: null, failed: false };
  private resetGeneration = 0;
  private retryPending = false;

  static getDerivedStateFromError(error: unknown): SectionBoundaryState {
    return { error, failed: true };
  }

  componentDidCatch(error: unknown, info: ErrorInfo) {
    console.error("Console section failed", error, info.componentStack);
  }

  componentDidUpdate(previous: SectionBoundaryProps) {
    if (!sameKeys(previous.resetKeys, this.props.resetKeys)) {
      ++this.resetGeneration;
      this.retryPending = false;
      if (this.state.failed) this.setState({ error: null, failed: false });
    } else if (
      this.state.failed &&
      !this.retryPending &&
      !sameKeys(previous.recoveryKeys, this.props.recoveryKeys)
    ) {
      ++this.resetGeneration;
      this.setState({ error: null, failed: false });
    }
  }

  componentWillUnmount() {
    ++this.resetGeneration;
  }

  private retry = async () => {
    const generation = ++this.resetGeneration;
    this.retryPending = true;
    try {
      await this.props.onReset?.();
      if (generation === this.resetGeneration) {
        this.retryPending = false;
        this.setState({ error: null, failed: false });
      }
    } catch (error) {
      if (generation === this.resetGeneration) {
        this.retryPending = false;
        this.setState({ error, failed: true });
      }
    }
  };

  render() {
    if (!this.state.failed) return this.props.children;
    return (
      <SectionFallback
        error={this.state.error}
        title={this.props.title}
        onRetry={this.retry}
      />
    );
  }
}
