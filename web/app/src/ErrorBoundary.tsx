import { Component, type ErrorInfo, type ReactNode } from 'react';

type Props = {
  children: ReactNode;
  fallback?: (err: Error, reset: () => void) => ReactNode;
};

type State = { err: Error | null };

// ErrorBoundary catches render errors in its subtree so a crashing
// component (e.g. a malformed tool_arguments payload) doesn't blank the
// whole screen. Callers can provide a custom fallback; the default is
// a muted inline message that stays out of the way.
export default class ErrorBoundary extends Component<Props, State> {
  state: State = { err: null };

  static getDerivedStateFromError(err: Error): State {
    return { err };
  }

  componentDidCatch(err: Error, info: ErrorInfo) {
    console.error('ErrorBoundary caught', err, info);
  }

  reset = () => this.setState({ err: null });

  render() {
    if (!this.state.err) return this.props.children;
    if (this.props.fallback) return this.props.fallback(this.state.err, this.reset);
    return (
      <div className="error-fallback">
        <strong>Something went wrong rendering this section.</strong>
        <div className="error-fallback__msg">{this.state.err.message}</div>
      </div>
    );
  }
}
