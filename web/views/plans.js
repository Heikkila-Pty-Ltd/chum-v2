/* ============================================================
   Plans — Grooming Workspace
   Chat-first UI for refining ideas into PR-level specs.
   ============================================================ */

(() => {

  // --- State machine ---
  // IDLE → SENDING → STREAMING → IDLE
  let uiState = 'IDLE';
  let activePlanId = null;
  let activeStream = null; // { planId, controller }
  let plans = [];
  let currentPlan = null;
  let chatScrollEl = null;
  let userScrolledUp = false;

  // --- API helpers ---
  const PlanAPI = {
    list: (project) => App.API.get(`/api/dashboard/plans/${project}`),
    get: (id) => App.API.get(`/api/dashboard/plan/${id}`),
    create: (body) => App.API.post('/api/dashboard/plans', body),
  };

  // --- Render entry point ---
  function render(viewport, project) {
    viewport.innerHTML = `<div class="plans-shell">
      <div class="plans-sidebar">
        <div class="plans-sidebar-header">
          <span class="plans-sidebar-title">Plans</span>
          <button class="plans-new-btn" data-plans-new>+ new</button>
        </div>
        <div class="plans-list" data-plans-list></div>
      </div>
      <div class="plans-main" data-plans-main>
        <div class="plans-welcome">
          <div class="plans-welcome-title">Plan Grooming</div>
          <div class="plans-welcome-hint">Select a plan or create a new one to start a grooming session. The planner will help you refine your idea into a PR-level spec.</div>
        </div>
      </div>
    </div>`;

    bindShellEvents(viewport, project);
    loadPlanList(project);
  }

  function bindShellEvents(viewport, project) {
    const newBtn = viewport.querySelector('[data-plans-new]');
    if (newBtn) {
      newBtn.addEventListener('click', () => createNewPlan(project));
    }
  }

  async function loadPlanList(project) {
    try {
      const data = await PlanAPI.list(project || App.currentProject || 'default');
      plans = data.plans || [];
      renderPlanList();
    } catch (err) {
      const listEl = document.querySelector('[data-plans-list]');
      if (listEl) listEl.innerHTML = `<div class="plans-list-empty">Failed to load plans</div>`;
    }
  }

  function renderPlanList() {
    const listEl = document.querySelector('[data-plans-list]');
    if (!listEl) return;

    if (plans.length === 0) {
      listEl.innerHTML = `<div class="plans-list-empty">No plans yet</div>`;
      return;
    }

    listEl.innerHTML = plans.map(p => `
      <div class="plans-list-item ${p.id === activePlanId ? 'active' : ''}" data-plan-id="${App.escapeHtml(p.id)}">
        <div class="plans-list-item-title">${App.escapeHtml(p.title || 'Untitled')}</div>
        <div class="plans-list-item-meta">${App.escapeHtml(p.status)} \u00b7 ${App.timeAgo(p.updated_at)}</div>
      </div>
    `).join('');

    listEl.querySelectorAll('.plans-list-item').forEach(el => {
      el.addEventListener('click', () => {
        const planId = el.dataset.planId;
        if (planId !== activePlanId) {
          abortActiveStream();
          openPlan(planId);
        }
      });
    });
  }

  async function createNewPlan(project) {
    try {
      const data = await PlanAPI.create({
        project: project || App.currentProject || 'default',
        title: 'New Plan',
      });
      await loadPlanList(project);
      openPlan(data.id);
    } catch (err) {
      console.error('Failed to create plan:', err);
    }
  }

  async function openPlan(planId) {
    activePlanId = planId;
    renderPlanList(); // update active highlight

    const mainEl = document.querySelector('[data-plans-main]');
    if (!mainEl) return;

    try {
      currentPlan = await PlanAPI.get(planId);
      renderPlanMain(mainEl);
    } catch (err) {
      mainEl.innerHTML = `<div class="plans-welcome"><div class="plans-welcome-hint">Failed to load plan: ${App.escapeHtml(err.message)}</div></div>`;
    }
  }

  function renderPlanMain(mainEl) {
    const plan = currentPlan;
    if (!plan) return;

    let conversation = [];
    try { conversation = JSON.parse(plan.conversation || '[]'); } catch {}

    mainEl.innerHTML = `
      <div class="plans-status-bar">
        <span class="plans-status-indicator" data-status="${App.escapeHtml(plan.status)}">${App.escapeHtml(plan.status)}</span>
        <span class="plans-status-title">${App.escapeHtml(plan.title)}</span>
      </div>
      <div class="plans-chat" role="log" aria-live="polite" data-plans-chat>
        ${conversation.length === 0
          ? `<div class="plans-chat-empty">Start by describing what you want to build.</div>`
          : conversation.map(renderMessage).join('')}
      </div>
      <div class="plans-quick-actions" data-plans-quick>
        <button class="plans-quick-btn" data-quick="Break this into tasks">break into tasks</button>
        <button class="plans-quick-btn" data-quick="Write the spec now">write spec</button>
        <button class="plans-quick-btn" data-quick="What are the edge cases?">edge cases</button>
      </div>
      <div class="plans-composer">
        <textarea class="plans-composer-input" data-plans-input
          placeholder="Describe your idea, answer questions, or ask for changes\u2026"
          rows="1"></textarea>
        <button class="plans-send-btn" data-plans-send>Send</button>
      </div>
    `;

    chatScrollEl = mainEl.querySelector('[data-plans-chat]');
    userScrolledUp = false;
    bindChatEvents(mainEl);
    scrollToBottom();
  }

  function renderMessage(msg) {
    if (msg.role === 'user') {
      return `<div class="plans-msg plans-msg-user">
        <div class="plans-msg-role">you</div>
        ${App.escapeHtml(msg.message)}
      </div>`;
    }
    // Assistant messages: render as HTML (simple markdown-ish rendering).
    return `<div class="plans-msg plans-msg-assistant">
      <div class="plans-msg-role">planner</div>
      ${simpleMarkdown(msg.message)}
    </div>`;
  }

  function bindChatEvents(mainEl) {
    const input = mainEl.querySelector('[data-plans-input]');
    const sendBtn = mainEl.querySelector('[data-plans-send]');

    if (sendBtn) {
      sendBtn.addEventListener('click', () => sendMessage(input));
    }

    if (input) {
      input.addEventListener('keydown', (e) => {
        e.stopPropagation(); // prevent app keyboard shortcuts
        if (e.key === 'Enter' && !e.shiftKey) {
          e.preventDefault();
          sendMessage(input);
        }
      });
      // Auto-resize textarea.
      input.addEventListener('input', () => {
        input.style.height = 'auto';
        input.style.height = Math.min(input.scrollHeight, 120) + 'px';
      });
    }

    // Quick action buttons.
    mainEl.querySelectorAll('[data-quick]').forEach(btn => {
      btn.addEventListener('click', () => {
        if (uiState !== 'IDLE') return;
        const text = btn.dataset.quick;
        if (input) input.value = text;
        sendMessage(input);
      });
    });

    // Track user scroll position.
    if (chatScrollEl) {
      chatScrollEl.addEventListener('scroll', () => {
        const { scrollTop, scrollHeight, clientHeight } = chatScrollEl;
        userScrolledUp = (scrollHeight - scrollTop - clientHeight) > 40;
      });
    }
  }

  async function sendMessage(input) {
    if (uiState !== 'IDLE') return;
    const message = input ? input.value.trim() : '';
    if (!message || !activePlanId) return;

    // 1. Capture and clear input.
    input.value = '';
    input.style.height = 'auto';

    // 2. Add user message to chat immediately.
    appendUserMessage(message);

    // 3. Create streaming assistant bubble.
    const assistantEl = appendStreamingBubble();

    // 4. Transition to SENDING.
    setUIState('SENDING');

    try {
      // 5. POST to groom endpoint with streaming.
      const controller = new AbortController();
      activeStream = { planId: activePlanId, controller };

      const res = await fetch(`/api/dashboard/plan/${activePlanId}/groom`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ message }),
        signal: controller.signal,
      });

      if (!res.ok) {
        throw new Error(`${res.status} ${res.statusText}`);
      }

      // 6. Transition to STREAMING.
      setUIState('STREAMING');

      // 7. Read SSE stream.
      const reader = res.body.getReader();
      const decoder = new TextDecoder();
      let buffer = '';
      let fullText = '';

      while (true) {
        const { done, value } = await reader.read();
        if (done) break;

        buffer += decoder.decode(value, { stream: true });
        const lines = buffer.split('\n');
        buffer = lines.pop(); // keep incomplete line in buffer

        for (const line of lines) {
          if (line.startsWith('data: ')) {
            try {
              const data = JSON.parse(line.slice(6));
              if (data.text) {
                fullText += data.text;
                // Use textContent for streaming (XSS-safe).
                assistantEl.querySelector('.plans-msg-content').textContent = fullText;
                if (!userScrolledUp) scrollToBottom();
              }
              if (data.error) {
                assistantEl.querySelector('.plans-msg-content').textContent = 'Error: ' + data.error;
              }
            } catch {
              // Skip malformed JSON lines.
            }
          }
        }
      }

      // 8. Final render with markdown after stream completes.
      assistantEl.classList.remove('plans-msg-streaming');
      assistantEl.querySelector('.plans-msg-content').innerHTML = simpleMarkdown(fullText);

    } catch (err) {
      if (err.name === 'AbortError') {
        assistantEl.querySelector('.plans-msg-content').textContent = '(cancelled)';
      } else {
        assistantEl.querySelector('.plans-msg-content').textContent = 'Error: ' + err.message;
      }
      assistantEl.classList.remove('plans-msg-streaming');
    } finally {
      activeStream = null;
      setUIState('IDLE');
    }
  }

  function appendUserMessage(text) {
    if (!chatScrollEl) return;
    // Remove empty state if present.
    const empty = chatScrollEl.querySelector('.plans-chat-empty');
    if (empty) empty.remove();

    const div = document.createElement('div');
    div.className = 'plans-msg plans-msg-user';
    div.innerHTML = `<div class="plans-msg-role">you</div>${App.escapeHtml(text)}`;
    chatScrollEl.appendChild(div);
    scrollToBottom();
  }

  function appendStreamingBubble() {
    if (!chatScrollEl) return document.createElement('div');

    const div = document.createElement('div');
    div.className = 'plans-msg plans-msg-assistant plans-msg-streaming';
    div.innerHTML = `<div class="plans-msg-role">planner</div><div class="plans-msg-content"></div>`;
    chatScrollEl.appendChild(div);
    scrollToBottom();
    return div;
  }

  function scrollToBottom() {
    if (chatScrollEl) {
      requestAnimationFrame(() => {
        chatScrollEl.scrollTop = chatScrollEl.scrollHeight;
      });
    }
  }

  function setUIState(newState) {
    uiState = newState;
    const input = document.querySelector('[data-plans-input]');
    const sendBtn = document.querySelector('[data-plans-send]');
    const quickBtns = document.querySelectorAll('[data-quick]');
    const isIdle = newState === 'IDLE';

    if (input) input.disabled = !isIdle;
    if (sendBtn) sendBtn.disabled = !isIdle;
    quickBtns.forEach(b => b.disabled = !isIdle);

    // Update aria-busy on chat container.
    if (chatScrollEl) chatScrollEl.setAttribute('aria-busy', (!isIdle).toString());
  }

  function abortActiveStream() {
    if (activeStream) {
      activeStream.controller.abort();
      activeStream = null;
    }
    setUIState('IDLE');
  }

  // --- Simple markdown renderer ---
  // Handles: headers, bold, code blocks, inline code, lists, paragraphs.
  // No external deps. Escapes HTML first for safety.
  function simpleMarkdown(text) {
    if (!text) return '';

    // Escape HTML entities.
    let html = text
      .replace(/&/g, '&amp;')
      .replace(/</g, '&lt;')
      .replace(/>/g, '&gt;');

    // Code blocks (```...```).
    html = html.replace(/```(\w*)\n([\s\S]*?)```/g, (_, lang, code) => {
      return `<pre><code>${code.trim()}</code></pre>`;
    });

    // Inline code (`...`).
    html = html.replace(/`([^`]+)`/g, '<code>$1</code>');

    // Headers.
    html = html.replace(/^### (.+)$/gm, '<h3>$1</h3>');
    html = html.replace(/^## (.+)$/gm, '<h2>$1</h2>');
    html = html.replace(/^# (.+)$/gm, '<h1>$1</h1>');

    // Bold.
    html = html.replace(/\*\*(.+?)\*\*/g, '<strong>$1</strong>');

    // Italic.
    html = html.replace(/\*(.+?)\*/g, '<em>$1</em>');

    // Unordered lists.
    html = html.replace(/^- (.+)$/gm, '<li>$1</li>');
    html = html.replace(/(<li>[\s\S]*?<\/li>)/g, (match) => {
      if (!match.startsWith('<ul>')) return `<ul>${match}</ul>`;
      return match;
    });
    // Clean up adjacent </ul><ul> pairs.
    html = html.replace(/<\/ul>\s*<ul>/g, '');

    // Ordered lists.
    html = html.replace(/^\d+\. (.+)$/gm, '<li>$1</li>');

    // Checkbox lists.
    html = html.replace(/^- \[x\] (.+)$/gm, '<li>\u2611 $1</li>');
    html = html.replace(/^- \[ \] (.+)$/gm, '<li>\u2610 $1</li>');

    // Tables (basic).
    html = html.replace(/^\|(.+)\|$/gm, (_, row) => {
      const cells = row.split('|').map(c => c.trim());
      if (cells.every(c => /^[-:]+$/.test(c))) return ''; // separator row
      const tag = 'td';
      return '<tr>' + cells.map(c => `<${tag}>${c}</${tag}>`).join('') + '</tr>';
    });
    html = html.replace(/(<tr>[\s\S]*?<\/tr>)/g, (match) => {
      if (!match.startsWith('<table>')) return `<table>${match}</table>`;
      return match;
    });
    html = html.replace(/<\/table>\s*<table>/g, '');

    // Paragraphs: double newlines become paragraph breaks.
    html = html.replace(/\n\n+/g, '</p><p>');
    // Single newlines become line breaks (but not inside pre/code).
    html = html.replace(/(?<!<\/pre>)\n(?!<)/g, '<br>');

    // Wrap in paragraph if not already wrapped.
    if (!html.startsWith('<')) html = '<p>' + html + '</p>';

    return html;
  }

  // --- Refresh ---
  function refresh(project) {
    // Don't refresh while streaming.
    if (uiState !== 'IDLE') return;
    loadPlanList(project);
  }

  App.registerView('plans', { render, refresh });
})();
