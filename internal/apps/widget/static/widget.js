// Kit website chat widget.
//
// Embed via <script src="https://kit/widget.js?token=wt_..." async></script>.
// The script bootstraps a floating bubble + slide-up panel into
// document.body, persists conversation state in localStorage, and
// talks to /widget/api/open and /widget/api/chat on whatever origin
// served this script.

(function () {
  'use strict';

  // Resolve our own origin from the currentScript src — that's where
  // the API endpoints live. We can't just use location.origin because
  // the host page is on a different domain (Wix, etc.).
  function scriptOrigin() {
    try {
      var s = document.currentScript;
      if (!s || !s.src) return '';
      return new URL(s.src).origin;
    } catch (e) { return ''; }
  }

  function scriptToken() {
    try {
      var s = document.currentScript;
      if (!s || !s.src) return '';
      return new URL(s.src).searchParams.get('token') || '';
    } catch (e) { return ''; }
  }

  var BASE = scriptOrigin();
  var TOKEN = scriptToken();

  if (!BASE || !TOKEN) {
    console.warn('kit-widget: missing base url or token in script src');
    return;
  }

  // localStorage keys. Visitor id is per-browser, conversation id
  // resets when the visitor starts a new chat.
  var KEY_VISITOR = 'kit_widget_visitor_id';
  var KEY_CONV = 'kit_widget_conversation_id';

  function uuid() {
    if (window.crypto && window.crypto.randomUUID) return window.crypto.randomUUID();
    // RFC 4122 v4 fallback
    return 'xxxxxxxx-xxxx-4xxx-yxxx-xxxxxxxxxxxx'.replace(/[xy]/g, function (c) {
      var r = Math.random() * 16 | 0, v = c === 'x' ? r : (r & 0x3 | 0x8);
      return v.toString(16);
    });
  }

  function ensure(key) {
    var v = localStorage.getItem(key);
    if (!v) { v = uuid(); localStorage.setItem(key, v); }
    return v;
  }

  function newConversation() {
    var v = uuid();
    localStorage.setItem(KEY_CONV, v);
    return v;
  }

  var visitorId = ensure(KEY_VISITOR);
  var conversationId = ensure(KEY_CONV);

  // Inject our CSS once. The file lives next to widget.js on the
  // server; we fetch and inline so the page only needs one script tag.
  function injectCSS() {
    if (document.getElementById('kit-widget-css')) return;
    var link = document.createElement('link');
    link.id = 'kit-widget-css';
    link.rel = 'stylesheet';
    link.href = BASE + '/widget.css';
    document.head.appendChild(link);
  }

  // Build the DOM tree once and re-attach if Wix's client-side router
  // wipes document.body on a navigation. The MutationObserver below
  // re-mounts when it sees our root removed.
  function buildRoot() {
    var root = document.createElement('div');
    root.id = 'kit-widget-root';
    root.innerHTML = ''
      + '<button class="kit-widget-bubble" aria-label="Open chat">'
      +   '<svg width="22" height="22" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">'
      +     '<path d="M21 15a2 2 0 0 1-2 2H7l-4 4V5a2 2 0 0 1 2-2h14a2 2 0 0 1 2 2z"/>'
      +   '</svg>'
      + '</button>'
      + '<div class="kit-widget-panel" hidden>'
      +   '<header class="kit-widget-header">'
      +     '<span class="kit-widget-title">Ask a question</span>'
      +     '<button class="kit-widget-new" type="button" aria-label="New chat">New chat</button>'
      +     '<button class="kit-widget-close" type="button" aria-label="Close">×</button>'
      +   '</header>'
      +   '<div class="kit-widget-messages" aria-live="polite"></div>'
      +   '<form class="kit-widget-form">'
      +     '<input class="kit-widget-input" type="text" placeholder="Type a question…" autocomplete="off" />'
      +     '<button class="kit-widget-send" type="submit" aria-label="Send">Send</button>'
      +   '</form>'
      + '</div>';
    return root;
  }

  var root, bubble, panel, msgs, form, input, status;
  var openedOnceThisConv = false;

  function attach() {
    if (document.getElementById('kit-widget-root')) return;
    injectCSS();
    root = buildRoot();
    document.body.appendChild(root);
    bubble = root.querySelector('.kit-widget-bubble');
    panel = root.querySelector('.kit-widget-panel');
    msgs = root.querySelector('.kit-widget-messages');
    form = root.querySelector('.kit-widget-form');
    input = root.querySelector('.kit-widget-input');

    bubble.addEventListener('click', togglePanel);
    root.querySelector('.kit-widget-close').addEventListener('click', closePanel);
    root.querySelector('.kit-widget-new').addEventListener('click', resetConversation);
    form.addEventListener('submit', onSubmit);
  }

  function togglePanel() {
    if (panel.hasAttribute('hidden')) openPanel();
    else closePanel();
  }

  function openPanel() {
    panel.removeAttribute('hidden');
    input.focus();
    if (!openedOnceThisConv) {
      openedOnceThisConv = true;
      callOpenEndpoint();
    }
  }

  function closePanel() {
    panel.setAttribute('hidden', '');
  }

  function resetConversation() {
    conversationId = newConversation();
    openedOnceThisConv = false;
    msgs.innerHTML = '';
    addSystemLine('Started a new chat.');
    // Fire open event for the new conversation immediately.
    openedOnceThisConv = true;
    callOpenEndpoint();
  }

  function callOpenEndpoint() {
    fetch(BASE + '/widget/api/open', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        token: TOKEN,
        conversation_id: conversationId,
        visitor_id: visitorId
      })
    }).catch(function (e) { console.warn('kit-widget: open failed', e); });
  }

  function addBubble(role, text) {
    var div = document.createElement('div');
    div.className = 'kit-widget-msg kit-widget-msg-' + role;
    if (role === 'assistant') {
      div.innerHTML = renderMarkdown(text);
    } else {
      div.textContent = text;
    }
    msgs.appendChild(div);
    msgs.scrollTop = msgs.scrollHeight;
    return div;
  }

  // Minimal markdown → HTML for assistant replies. Handles the
  // subset the LLM actually emits: bold, italic, inline code, bullet
  // and numbered lists, headings, paragraph breaks, and links. Input
  // is HTML-escaped first so any markup in the source is rendered as
  // literal text. We deliberately don't include images, raw HTML,
  // blockquotes, or fenced code blocks — they'd add complexity for
  // negligible Q&A benefit.
  function renderMarkdown(src) {
    var esc = src.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;');

    // Split into blocks on blank lines.
    var blocks = esc.split(/\n\s*\n/);
    var html = '';
    for (var i = 0; i < blocks.length; i++) {
      var block = blocks[i].trim();
      if (!block) continue;

      // Heading: ### Title
      var hMatch = block.match(/^(#{1,6})\s+(.+)$/);
      if (hMatch && !/\n/.test(block)) {
        var level = Math.min(6, hMatch[1].length);
        html += '<h' + level + '>' + inlineMarkdown(hMatch[2]) + '</h' + level + '>';
        continue;
      }

      // Bulleted list: lines starting with -, *, or +
      if (/^([-*+])\s+/.test(block)) {
        var items = block.split(/\n/).filter(function (l) { return /^([-*+])\s+/.test(l); });
        html += '<ul>';
        for (var j = 0; j < items.length; j++) {
          html += '<li>' + inlineMarkdown(items[j].replace(/^([-*+])\s+/, '')) + '</li>';
        }
        html += '</ul>';
        continue;
      }

      // Numbered list: lines starting with "1. " etc.
      if (/^\d+\.\s+/.test(block)) {
        var numItems = block.split(/\n/).filter(function (l) { return /^\d+\.\s+/.test(l); });
        html += '<ol>';
        for (var k = 0; k < numItems.length; k++) {
          html += '<li>' + inlineMarkdown(numItems[k].replace(/^\d+\.\s+/, '')) + '</li>';
        }
        html += '</ol>';
        continue;
      }

      // Paragraph — single newlines become <br>.
      html += '<p>' + inlineMarkdown(block).replace(/\n/g, '<br>') + '</p>';
    }
    return html;
  }

  function inlineMarkdown(s) {
    // Inline code: `text` — done first so other rules don't touch it.
    s = s.replace(/`([^`\n]+)`/g, '<code>$1</code>');
    // Bold: **text**
    s = s.replace(/\*\*([^*\n]+)\*\*/g, '<strong>$1</strong>');
    // Italic: *text* (avoid eating bold's leftover *)
    s = s.replace(/(^|[^*])\*([^*\n]+)\*(?!\*)/g, '$1<em>$2</em>');
    // Markdown links: [text](url) — restrict to http(s):// for safety.
    s = s.replace(/\[([^\]]+)\]\((https?:\/\/[^\s)]+)\)/g, function (_, txt, href) {
      return '<a href="' + href + '" target="_blank" rel="noopener noreferrer">' + txt + '</a>';
    });
    return s;
  }

  function addSystemLine(text) {
    var div = document.createElement('div');
    div.className = 'kit-widget-system';
    div.textContent = text;
    msgs.appendChild(div);
    msgs.scrollTop = msgs.scrollHeight;
  }

  function setStatus(text) {
    if (status) status.remove();
    if (!text) { status = null; return; }
    status = document.createElement('div');
    status.className = 'kit-widget-status';
    status.textContent = text;
    msgs.appendChild(status);
    msgs.scrollTop = msgs.scrollHeight;
  }

  function clearStatus() {
    if (status) { status.remove(); status = null; }
  }

  function onSubmit(e) {
    e.preventDefault();
    var text = input.value.trim();
    if (!text) return;
    addBubble('user', text);
    input.value = '';
    input.disabled = true;
    form.querySelector('.kit-widget-send').disabled = true;
    setStatus('Thinking…');
    sendChat(text)
      .catch(function (err) {
        clearStatus();
        addSystemLine('Sorry, something went wrong.');
        console.warn('kit-widget: chat failed', err);
      })
      .finally(function () {
        input.disabled = false;
        form.querySelector('.kit-widget-send').disabled = false;
        input.focus();
      });
  }

  // SSE consumer using fetch + ReadableStream (EventSource is GET-only
  // and we want POST so the token lives in the body, not the URL).
  function sendChat(text) {
    return fetch(BASE + '/widget/api/chat', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json', 'Accept': 'text/event-stream' },
      body: JSON.stringify({
        token: TOKEN,
        conversation_id: conversationId,
        visitor_id: visitorId,
        message: text
      })
    }).then(function (res) {
      if (!res.ok) throw new Error('HTTP ' + res.status);
      var reader = res.body.getReader();
      var decoder = new TextDecoder('utf-8');
      var buf = '';
      function pump() {
        return reader.read().then(function (r) {
          if (r.done) return;
          buf += decoder.decode(r.value, { stream: true });
          var idx;
          while ((idx = buf.indexOf('\n\n')) >= 0) {
            handleEvent(buf.slice(0, idx));
            buf = buf.slice(idx + 2);
          }
          return pump();
        });
      }
      return pump();
    });
  }

  function handleEvent(raw) {
    var event = '';
    var data = '';
    raw.split('\n').forEach(function (line) {
      if (line.indexOf('event:') === 0) event = line.slice(6).trim();
      else if (line.indexOf('data:') === 0) data += line.slice(5).trim();
    });
    var parsed = {};
    try { parsed = JSON.parse(data); } catch (e) { /* ignore */ }
    switch (event) {
      case 'status':
        setStatus(parsed.status === 'cancelled' ? 'Cancelled.' : 'Thinking…');
        break;
      case 'tool':
        setStatus('Looking that up…');
        break;
      case 'message':
        clearStatus();
        if (parsed.text) addBubble('assistant', parsed.text);
        break;
      case 'done':
        clearStatus();
        break;
      case 'error':
        clearStatus();
        addSystemLine(parsed.message || 'Sorry, something went wrong.');
        break;
    }
  }

  // Watch for Wix client-side navigation that wipes document.body.
  // The bubble lives outside React/Vue trees so most navigations
  // leave it alone — but a full re-render of body would drop us.
  function watchMounted() {
    var obs = new MutationObserver(function () {
      if (!document.getElementById('kit-widget-root')) attach();
    });
    obs.observe(document.body, { childList: true });
  }

  if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', function () {
      attach(); watchMounted();
    });
  } else {
    attach(); watchMounted();
  }
})();
