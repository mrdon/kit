import { Component, type ErrorInfo, type ReactNode } from 'react';

// The net under the phone.
//
// React unmounts the WHOLE tree on an uncaught render error, so without this
// any one-off bug in any one screen is a blank phone in a dark bar, and the
// only way back is for somebody to work out that they should pull down to
// reload. That is the same thing the room reports as "it froze", whatever
// actually caused it, and it is the reason a client bug here costs a table
// their night rather than a question.
//
// A class component because that is still the only way to catch a render
// error in React; there is no hook for it.
//
// It offers a button rather than reloading by itself. An automatic reload on
// a deterministic error is an infinite loop the person cannot escape, and a
// phone that reloads itself repeatedly is harder to diagnose than one holding
// still with a message on it.
export class Boundary extends Component<{ children: ReactNode }, { failed: boolean }> {
  constructor(props: { children: ReactNode }) {
    super(props);
    this.state = { failed: false };
  }

  static getDerivedStateFromError() {
    return { failed: true };
  }

  componentDidCatch(error: Error, info: ErrorInfo) {
    // Goes to the phone's own console, which is where somebody debugging with
    // a cable will look. There is no client error reporting in this app and
    // this is not the change that should add one.
    console.error('[trivia] render failed', error, info.componentStack);
  }

  render() {
    if (!this.state.failed) return this.props.children;
    return (
      <div className="body">
        <h1>Lost the thread</h1>
        <p className="sub" style={{ textAlign: 'center' }}>
          Your score and your answers are safe on the server. Tap to pick the game back up.
        </p>
        <button className="btn" type="button" onClick={() => window.location.reload()}>
          Reload
        </button>
      </div>
    );
  }
}
