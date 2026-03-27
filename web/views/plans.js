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
  let activeEventSource = null; // SSE connection to session stream
  let activeStreamingBubble = null; // current streaming assistant bubble
  let plans = [];
  let currentPlan = null;
  let chatScrollEl = null;
  let userScrolledUp = false;
  let cachedShell = null; // cached DOM node to survive tab switches
  let selectedTaskRef = null; // currently selected draft task for refine flow

  // --- Render entry point ---
  function render(viewport, project) {
    // Reattach cached DOM if we already built it (tab switch round-trip).
    if (cachedShell && cachedShell.parentNode !== viewport) {
      viewport.innerHTML = '';
      viewport.appendChild(cachedShell);
      chatScrollEl = cachedShell.querySelector('[data-plans-chat]');
      loadPlanList(project);
      return;
    }

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

    cachedShell = viewport.querySelector('.plans-shell');
    bindShellEvents(viewport, project);
    loadPlanList(project).then(() => {
      if (activePlanId) openPlan(activePlanId);
    });
  }

  function bindShellEvents(viewport, project) {
    const newBtn = viewport.querySelector('[data-plans-new]');
    if (newBtn) {
      newBtn.addEventListener('click', () => createNewPlan(project));
    }
  }

  async function loadPlanList(project) {
    try {
      const data = await App.API.plans(project || App.currentProject || 'default');
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
      const data = await App.API.planCreate({
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
    // Clean up previous session connection.
    disconnectSession();

    activePlanId = planId;
    selectedTaskRef = null;
    renderPlanList();

    const mainEl = document.querySelector('[data-plans-main]');
    if (!mainEl) return;

    try {
      currentPlan = await App.API.plan(planId);
      renderPlanMain(mainEl);

      // Try to create/connect to an interactive session.
      connectSession(planId);
    } catch (err) {
      mainEl.innerHTML = `<div class="plans-welcome"><div class="plans-welcome-hint">Failed to load plan: ${App.escapeHtml(err.message)}</div></div>`;
    }
  }

  // --- Session management ---

  async function connectSession(planId) {
    try {
      // Create session (idempotent).
      await fetch(`/api/dashboard/plan/${planId}/session`, { method: 'POST' });

      // Connect SSE stream.
      const es = new EventSource(`/api/dashboard/plan/${planId}/session/stream`);
      activeEventSource = es;

      es.addEventListener('token', (e) => {
        const data = JSON.parse(e.data);
        if (!activeStreamingBubble) {
          activeStreamingBubble = appendStreamingBubble();
          setUIState('STREAMING');
        }
        const contentEl = activeStreamingBubble.querySelector('.plans-msg-content');
        if (contentEl) {
          contentEl.innerHTML += App.escapeHtml(data.text);
        }
        if (!userScrolledUp) scrollToBottom();
      });

      es.addEventListener('turn_complete', () => {
        if (activeStreamingBubble) {
          activeStreamingBubble.classList.remove('plans-msg-streaming');
          // Re-render with markdown.
          const contentEl = activeStreamingBubble.querySelector('.plans-msg-content');
          if (contentEl) {
            contentEl.innerHTML = simpleMarkdown(contentEl.textContent || '');
          }
          activeStreamingBubble = null;
        }
        setUIState('IDLE');
      });

      es.addEventListener('session_error', (e) => {
        const data = JSON.parse(e.data);
        if (activeStreamingBubble) {
          activeStreamingBubble.classList.remove('plans-msg-streaming');
          activeStreamingBubble.querySelector('.plans-msg-content').textContent = 'Error: ' + data.message;
          activeStreamingBubble = null;
        }
        if (data.recoverable === 'true') {
          setUIState('IDLE');
        }
      });

      es.addEventListener('session_destroyed', () => {
        appendSystemMessage('Session ended.');
        setUIState('IDLE');
        disconnectSession();
      });

      es.addEventListener('heartbeat', () => {
        // Keep-alive, no action needed.
      });

      es.onerror = () => {
        if (es.readyState === EventSource.CLOSED) {
          // Reconnect after a short delay.
          setTimeout(() => {
            if (activePlanId === planId) connectSession(planId);
          }, 2000);
        }
      };
    } catch (err) {
      // Session creation failed — fall back to legacy interview mode.
      console.warn('Session creation failed, using legacy mode:', err);
    }
  }

  function disconnectSession() {
    if (activeEventSource) {
      activeEventSource.close();
      activeEventSource = null;
    }
    activeStreamingBubble = null;
  }

  function appendSystemMessage(text) {
    if (!chatScrollEl) return;
    const div = document.createElement('div');
    div.className = 'plans-msg plans-msg-system';
    div.textContent = text;
    chatScrollEl.appendChild(div);
    scrollToBottom();
  }

  // Active main-area tab: 'chat' or 'document'
  let activeMainTab = 'chat';

  function renderPlanMain(mainEl) {
    const plan = currentPlan;
    if (!plan) return;

    let conversation = [];
    try {
      const raw = plan.conversation || '[]';
      conversation = typeof raw === 'string' ? JSON.parse(raw) : raw;
    } catch {}

    const draftTasks = parseDraftTasks(plan);
    const hasDoc = !!(plan.working_markdown || plan.brief_markdown);

    // If plan has a document but no conversation, default to document tab
    if (hasDoc && conversation.length === 0 && activeMainTab === 'chat') {
      activeMainTab = 'document';
    }

    mainEl.innerHTML = `
      <div class="plans-status-bar">
        <span class="plans-status-indicator" data-status="${App.escapeHtml(plan.status)}">${App.escapeHtml(plan.status)}</span>
        <span class="plans-status-title">${App.escapeHtml(plan.title)}</span>
        <div class="plans-main-tabs">
          <button class="plans-main-tab ${activeMainTab === 'chat' ? 'active' : ''}" data-main-tab="chat">Chat</button>
          <button class="plans-main-tab ${activeMainTab === 'document' ? 'active' : ''}" data-main-tab="document">Plan${hasDoc ? '' : ' (empty)'}</button>
        </div>
        <button class="plans-extract-btn" data-plans-extract title="Extract structured plan from conversation">Extract Plan</button>
      </div>

      <div class="plans-main-pane ${activeMainTab === 'chat' ? 'active' : ''}" data-main-pane="chat">
        <div class="plans-chat" role="log" aria-live="polite" data-plans-chat>
          ${conversation.length === 0
            ? `<div class="plans-chat-empty">Start by describing what you want to build.</div>`
            : conversation.map(renderMessage).join('')}
        </div>
      </div>

      <div class="plans-main-pane ${activeMainTab === 'document' ? 'active' : ''}" data-main-pane="document">
        <div class="plans-document" data-plans-document>
          ${renderPlanDocument(plan)}
        </div>
      </div>

      <div data-plans-preview-container>
        ${renderTaskPreview(draftTasks, plan.status)}
      </div>
      <div data-plans-pipeline-container>
        ${renderPipelineActions(plan.status, draftTasks.length)}
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
    bindMainTabEvents(mainEl);
    bindChatEvents(mainEl);
    bindPipelineEvents(mainEl);
    bindTaskPreviewEvents(mainEl);
    scrollToBottom();
  }

  // --- Plan document view ---
  function renderPlanDocument(plan) {
    const brief = plan.brief_markdown || '';
    const working = plan.working_markdown || '';

    if (!brief && !working) {
      return `<div class="plans-doc-empty">
        <div class="plans-doc-empty-title">No plan document yet</div>
        <div class="plans-doc-empty-hint">Use the chat to describe your idea, then ask to "write the spec" or "decompose into tasks". The structured plan will appear here.</div>
      </div>`;
    }

    let html = '';

    if (brief) {
      html += `<div class="plans-doc-section">
        <div class="plans-doc-section-label">Brief</div>
        <div class="plans-doc-content">${simpleMarkdown(brief)}</div>
      </div>`;
    }

    if (working) {
      html += `<div class="plans-doc-section">
        ${brief ? '<div class="plans-doc-section-label">Working Spec</div>' : ''}
        <div class="plans-doc-content">${simpleMarkdown(working)}</div>
      </div>`;
    }

    // Structured analysis (JSON) — render if present
    let structured = null;
    try {
      const raw = plan.structured || '{}';
      structured = typeof raw === 'string' ? JSON.parse(raw) : raw;
      if (structured && Object.keys(structured).length === 0) structured = null;
    } catch { structured = null; }

    if (structured) {
      html += `<div class="plans-doc-section">
        <div class="plans-doc-section-label">Structured Analysis</div>
        <div class="plans-doc-content"><pre><code>${App.escapeHtml(JSON.stringify(structured, null, 2))}</code></pre></div>
      </div>`;
    }

    return html;
  }

  function bindMainTabEvents(mainEl) {
    mainEl.querySelectorAll('[data-main-tab]').forEach(tab => {
      tab.addEventListener('click', () => {
        activeMainTab = tab.dataset.mainTab;
        mainEl.querySelectorAll('.plans-main-tab').forEach(t => t.classList.toggle('active', t.dataset.mainTab === activeMainTab));
        mainEl.querySelectorAll('.plans-main-pane').forEach(p => p.classList.toggle('active', p.dataset.mainPane === activeMainTab));
      });
    });

    const extractBtn = mainEl.querySelector('[data-plans-extract]');
    if (extractBtn) {
      extractBtn.addEventListener('click', async () => {
        if (!activePlanId || uiState !== 'IDLE') return;
        extractBtn.disabled = true;
        extractBtn.textContent = 'Extracting\u2026';
        try {
          await fetch(`/api/dashboard/plan/${activePlanId}/session/extract`, { method: 'POST' });
          appendSystemMessage('Extracting structured plan\u2026');
        } catch (err) {
          appendSystemMessage('Failed to start extraction: ' + err.message);
        } finally {
          extractBtn.disabled = false;
          extractBtn.textContent = 'Extract Plan';
        }
      });
    }
  }

  // --- Re-render pipeline + preview + document in-place (called during streaming) ---
  function renderPipeline() {
    if (!currentPlan) return;
    const draftTasks = parseDraftTasks(currentPlan);

    const previewContainer = document.querySelector('[data-plans-preview-container]');
    if (previewContainer) {
      previewContainer.innerHTML = renderTaskPreview(draftTasks, currentPlan.status);
      const mainEl = previewContainer.closest('[data-plans-main]');
      if (mainEl) bindTaskPreviewEvents(mainEl);
    }

    const pipelineContainer = document.querySelector('[data-plans-pipeline-container]');
    if (pipelineContainer) {
      pipelineContainer.innerHTML = renderPipelineActions(currentPlan.status, draftTasks.length);
      const mainEl = pipelineContainer.closest('[data-plans-main]');
      if (mainEl) bindPipelineEvents(mainEl);
    }

    // Update document pane if visible
    const docEl = document.querySelector('[data-plans-document]');
    if (docEl) {
      docEl.innerHTML = renderPlanDocument(currentPlan);
    }
  }

  function parseDraftTasks(plan) {
    try {
      const raw = plan.draft_tasks || '[]';
      return typeof raw === 'string' ? JSON.parse(raw) : raw;
    } catch { return []; }
  }

  // --- Pipeline actions (decompose → approve → materialize) ---

  function renderPipelineActions(status, taskCount) {
    const actions = [];

    if (['grooming', 'needs_input'].includes(status)) {
      actions.push(`<button class="plans-pipeline-btn" data-pipeline="decompose">Decompose into tasks</button>`);
    }
    if (status === 'decomposed') {
      actions.push(`<button class="plans-pipeline-btn plans-pipeline-btn--approve" data-pipeline="approve">Approve ${taskCount} tasks</button>`);
      actions.push(`<button class="plans-pipeline-btn" data-pipeline="decompose">Re-decompose</button>`);
    }
    if (status === 'approved') {
      actions.push(`<button class="plans-pipeline-btn plans-pipeline-btn--materialize" data-pipeline="materialize">Materialize (create work items)</button>`);
      actions.push(`<button class="plans-pipeline-btn" data-pipeline="decompose">Re-decompose</button>`);
    }
    if (status === 'materialized') {
      actions.push(`<div class="plans-pipeline-done">Materialized \u2014 tasks created and queued for execution.</div>`);
    }

    if (actions.length === 0) return '';
    return `<div class="plans-pipeline-bar" data-plans-pipeline>${actions.join('')}</div>`;
  }

  // --- Rich Task Preview ---

  function renderTaskPreview(draftTasks, status) {
    if (!draftTasks || draftTasks.length === 0) return '';
    if (!['decomposed', 'approved', 'materialized'].includes(status)) return '';

    const totalEst = draftTasks.reduce((s, t) => s + (t.estimate_minutes || 0), 0);
    const maxBatch = Math.max(0, ...draftTasks.map(t => t.batch || 0));

    const byRef = {};
    draftTasks.forEach(t => { byRef[t.ref] = t; });

    const summary = [];
    summary.push(`${draftTasks.length} task${draftTasks.length !== 1 ? 's' : ''}`);
    if (totalEst > 0) summary.push(`${totalEst}m total`);
    summary.push(`${maxBatch + 1} batch${maxBatch > 0 ? 'es' : ''}`);

    return `<div class="plans-task-preview" data-plans-tasks>
      <div class="plans-task-preview-header">
        <div class="plans-task-preview-tabs">
          <button class="plans-preview-tab active" data-preview-tab="tree">Tasks</button>
          <button class="plans-preview-tab" data-preview-tab="graph">Graph</button>
        </div>
        <div class="plans-task-preview-summary">
          ${summary.map(s => `<span>${s}</span>`).join('')}
        </div>
      </div>
      <div class="plans-preview-body">
        <div class="plans-preview-pane plans-preview-pane-tree active" data-preview-pane="tree">
          ${renderTaskTree(draftTasks, byRef)}
        </div>
        <div class="plans-preview-pane plans-preview-pane-graph" data-preview-pane="graph">
          <div class="plans-dep-graph" data-plans-dep-graph></div>
        </div>
      </div>
      ${renderSelectedTaskDetail(byRef)}
    </div>`;
  }

  function renderTaskTree(draftTasks, byRef) {
    // Group tasks by batch for display
    const batches = {};
    draftTasks.forEach(t => {
      const b = t.batch || 0;
      if (!batches[b]) batches[b] = [];
      batches[b].push(t);
    });
    const batchKeys = Object.keys(batches).sort((a, b) => Number(a) - Number(b));
    const showBatchHeaders = batchKeys.length > 1;

    let rows = '';
    for (const bk of batchKeys) {
      if (showBatchHeaders) {
        rows += `<tr class="plans-task-batch-header"><td colspan="4">Batch ${Number(bk) + 1}</td></tr>`;
      }
      for (const t of batches[bk]) {
        const deps = (t.depends_on || []).join(', ') || '\u2014';
        const isSelected = t.ref === selectedTaskRef;

        rows += `<tr class="plans-task-row ${isSelected ? 'plans-task-selected' : ''}" data-task-ref="${App.escapeHtml(t.ref)}">
          <td class="plans-task-ref">${App.escapeHtml(t.ref)}</td>
          <td>${App.escapeHtml(t.title)}</td>
          <td class="plans-task-est">${t.estimate_minutes ? t.estimate_minutes + 'm' : '\u2014'}</td>
          <td class="plans-task-deps">${App.escapeHtml(deps)}</td>
        </tr>`;
      }
    }

    return `<table class="plans-task-table">
      <thead><tr><th>Ref</th><th>Title</th><th>Est</th><th>Deps</th></tr></thead>
      <tbody>${rows}</tbody>
    </table>`;
  }

  function renderSelectedTaskDetail(byRef) {
    if (!selectedTaskRef || !byRef[selectedTaskRef]) return '';
    const t = byRef[selectedTaskRef];

    return `<div class="plans-selected-task" data-selected-task>
      <div class="plans-selected-header">
        <span class="plans-selected-ref">${App.escapeHtml(t.ref)}</span>
        <span class="plans-selected-title">${App.escapeHtml(t.title)}</span>
        <button class="plans-selected-close" data-deselect-task>\u00d7</button>
      </div>
      ${t.description ? `<div class="plans-selected-section"><div class="plans-selected-label">Description</div><div class="plans-selected-body">${simpleMarkdown(t.description)}</div></div>` : ''}
      ${t.acceptance ? `<div class="plans-selected-section"><div class="plans-selected-label">Acceptance Criteria</div><div class="plans-selected-body">${simpleMarkdown(t.acceptance)}</div></div>` : ''}
      ${t.estimate_minutes ? `<div class="plans-selected-section"><div class="plans-selected-label">Estimate</div><div class="plans-selected-body">${t.estimate_minutes}m</div></div>` : ''}
      ${(t.depends_on || []).length ? `<div class="plans-selected-section"><div class="plans-selected-label">Depends On</div><div class="plans-selected-body">${t.depends_on.map(d => App.escapeHtml(d)).join(', ')}</div></div>` : ''}
      <div class="plans-selected-actions">
        <button class="plans-refine-btn" data-refine-task="${App.escapeHtml(t.ref)}">Refine this task</button>
      </div>
    </div>`;
  }

  function renderDepGraph(container, draftTasks) {
    if (!draftTasks || draftTasks.length === 0) return;
    if (typeof d3 === 'undefined' || typeof dagre === 'undefined') {
      container.innerHTML = '<div class="plans-graph-unavailable">Graph library not loaded</div>';
      return;
    }

    const width = container.clientWidth || 400;
    if (draftTasks.length === 0) return;
    const leafTasks = draftTasks;

    const NODE_W = 120, NODE_H = 28;

    // Build dagre graph for layout
    const g = new dagre.graphlib.Graph();
    g.setGraph({ rankdir: 'LR', nodesep: 20, ranksep: 40, marginx: 10, marginy: 10 });
    g.setDefaultEdgeLabel(() => ({}));

    leafTasks.forEach(t => {
      g.setNode(t.ref, { width: NODE_W, height: NODE_H, label: App.truncate(t.title, 25) });
    });

    const refSet = new Set(leafTasks.map(t => t.ref));
    leafTasks.forEach(t => {
      (t.depends_on || []).forEach(dep => {
        if (refSet.has(dep)) g.setEdge(dep, t.ref);
      });
    });

    dagre.layout(g);

    // Render with D3
    const graphBounds = g.graph();
    const graphW = graphBounds.width || width;
    const graphH = graphBounds.height || 200;
    const scale = Math.min(1, (width - 20) / graphW, 180 / graphH);
    const svgH = graphH * scale + 20;

    container.innerHTML = '';
    const svg = d3.select(container).append('svg').attr('width', width).attr('height', svgH);

    // Arrow marker
    svg.append('defs').append('marker')
      .attr('id', 'plans-arrow').attr('viewBox', '0 0 10 10')
      .attr('refX', 10).attr('refY', 5)
      .attr('markerWidth', 6).attr('markerHeight', 6)
      .attr('orient', 'auto')
      .append('path').attr('d', 'M0,0 L10,5 L0,10 Z').attr('fill', 'var(--border-emphasis)');

    const inner = svg.append('g')
      .attr('transform', `translate(${(width - graphW * scale) / 2},10) scale(${scale})`);

    // Edges
    g.edges().forEach(e => {
      const edge = g.edge(e);
      const line = d3.line().x(p => p.x).y(p => p.y).curve(d3.curveBasis);
      inner.append('path')
        .attr('d', line(edge.points))
        .attr('fill', 'none')
        .attr('stroke', 'var(--border-emphasis)')
        .attr('stroke-width', 1)
        .attr('marker-end', 'url(#plans-arrow)');
    });

    // Nodes
    g.nodes().forEach(id => {
      const n = g.node(id);
      const ng = inner.append('g').attr('transform', `translate(${n.x - NODE_W/2},${n.y - NODE_H/2})`);
      ng.append('rect')
        .attr('width', NODE_W).attr('height', NODE_H)
        .attr('rx', 3).attr('fill', 'var(--surface-3)')
        .attr('stroke', 'var(--border-default)').attr('stroke-width', 1);
      ng.append('text')
        .attr('x', NODE_W / 2).attr('y', NODE_H / 2 + 4)
        .attr('text-anchor', 'middle')
        .attr('fill', 'var(--text-primary)')
        .attr('font-size', '10px')
        .attr('font-family', 'var(--font-display)')
        .text(n.label);
    });
  }

  function bindTaskPreviewEvents(mainEl) {
    // Tab switching (tree/graph)
    mainEl.querySelectorAll('[data-preview-tab]').forEach(tab => {
      tab.addEventListener('click', () => {
        const target = tab.dataset.previewTab;
        mainEl.querySelectorAll('.plans-preview-tab').forEach(t => t.classList.toggle('active', t.dataset.previewTab === target));
        mainEl.querySelectorAll('.plans-preview-pane').forEach(p => p.classList.toggle('active', p.dataset.previewPane === target));

        // Lazy-render graph on first switch
        if (target === 'graph') {
          const graphEl = mainEl.querySelector('[data-plans-dep-graph]');
          if (graphEl && !graphEl.hasChildNodes()) {
            renderDepGraph(graphEl, parseDraftTasks(currentPlan));
          }
        }
      });
    });

    // Task row click → select
    mainEl.querySelectorAll('.plans-task-row').forEach(row => {
      row.addEventListener('click', () => {
        const ref = row.dataset.taskRef;
        selectedTaskRef = (selectedTaskRef === ref) ? null : ref;
        // Re-render preview to show/hide detail
        const previewContainer = mainEl.querySelector('[data-plans-preview-container]');
        if (previewContainer && currentPlan) {
          const draftTasks = parseDraftTasks(currentPlan);
          previewContainer.innerHTML = renderTaskPreview(draftTasks, currentPlan.status);
          bindTaskPreviewEvents(mainEl);
          // Re-render graph if graph tab was active
          const activeTab = previewContainer.querySelector('.plans-preview-tab.active');
          if (activeTab && activeTab.dataset.previewTab === 'graph') {
            const graphEl = previewContainer.querySelector('[data-plans-dep-graph]');
            if (graphEl) renderDepGraph(graphEl, draftTasks);
          }
        }
      });
    });

    // Deselect button
    mainEl.querySelectorAll('[data-deselect-task]').forEach(btn => {
      btn.addEventListener('click', (e) => {
        e.stopPropagation();
        selectedTaskRef = null;
        const previewContainer = mainEl.querySelector('[data-plans-preview-container]');
        if (previewContainer && currentPlan) {
          const draftTasks = parseDraftTasks(currentPlan);
          previewContainer.innerHTML = renderTaskPreview(draftTasks, currentPlan.status);
          bindTaskPreviewEvents(mainEl);
        }
      });
    });

    // Refine button → populate composer with context
    mainEl.querySelectorAll('[data-refine-task]').forEach(btn => {
      btn.addEventListener('click', () => {
        const ref = btn.dataset.refineTask;
        const draftTasks = parseDraftTasks(currentPlan);
        const task = draftTasks.find(t => t.ref === ref);
        if (!task) return;

        const input = mainEl.querySelector('[data-plans-input]');
        if (input) {
          input.value = `Regarding task ${ref} ("${task.title}"): `;
          input.focus();
          input.style.height = 'auto';
          input.style.height = Math.min(input.scrollHeight, 120) + 'px';
        }
      });
    });

    // Pipeline events within preview
    mainEl.querySelectorAll('[data-pipeline]').forEach(btn => {
      btn.addEventListener('click', () => {
        if (btn.disabled) return;
        executePipelineAction(btn.dataset.pipeline, btn);
      });
    });
  }

  function bindPipelineEvents(mainEl) {
    mainEl.querySelectorAll('[data-plans-pipeline-container] [data-pipeline]').forEach(btn => {
      btn.addEventListener('click', () => {
        if (btn.disabled) return;
        executePipelineAction(btn.dataset.pipeline, btn);
      });
    });
  }

  async function executePipelineAction(action, btn) {
    if (!activePlanId) return;
    btn.disabled = true;
    const origText = btn.textContent;
    btn.textContent = action === 'decompose' ? 'Decomposing\u2026' :
                      action === 'approve' ? 'Approving\u2026' : 'Materializing\u2026';

    try {
      const apiFn = action === 'decompose' ? App.API.planDecompose
                   : action === 'approve' ? App.API.planApprove
                   : App.API.planMaterialize;

      let result = await apiFn(activePlanId);
      if (action === 'approve' && result.plan) result = result.plan;

      currentPlan = result;
      selectedTaskRef = null;
      const mainEl = document.querySelector('[data-plans-main]');
      if (mainEl) renderPlanMain(mainEl);
      loadPlanList(currentPlan.project || App.currentProject);
    } catch (err) {
      btn.textContent = 'Error: ' + err.message;
      setTimeout(() => { btn.textContent = origText; btn.disabled = false; }, 5000);
    }
  }

  function renderMessage(msg) {
    if (msg.role === 'user') {
      return `<div class="plans-msg plans-msg-user">
        <div class="plans-msg-role">you</div>
        ${App.escapeHtml(msg.message)}
      </div>`;
    }
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
        e.stopPropagation();
        if (e.key === 'Enter' && !e.shiftKey) {
          e.preventDefault();
          sendMessage(input);
        }
      });
      input.addEventListener('input', () => {
        input.style.height = 'auto';
        input.style.height = Math.min(input.scrollHeight, 120) + 'px';
      });
    }

    mainEl.querySelectorAll('[data-quick]').forEach(btn => {
      btn.addEventListener('click', () => {
        if (uiState !== 'IDLE') return;
        const text = btn.dataset.quick;
        if (input) input.value = text;
        sendMessage(input);
      });
    });

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

    input.value = '';
    input.style.height = 'auto';

    appendUserMessage(message);
    setUIState('SENDING');

    // If we have an active session, use the session endpoint.
    if (activeEventSource) {
      try {
        const res = await fetch(`/api/dashboard/plan/${activePlanId}/session/message`, {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ message }),
        });

        if (res.status === 409) {
          appendSystemMessage('Claude is currently responding\u2026');
          setUIState('IDLE');
          return;
        }

        if (!res.ok) {
          throw new Error(`${res.status} ${res.statusText}`);
        }

        // Response will arrive via SSE — stay in SENDING state until token event.
      } catch (err) {
        appendSystemMessage('Error: ' + err.message);
        setUIState('IDLE');
      }
      return;
    }

    // Fallback: legacy interview mode (no active session).
    const assistantEl = appendStreamingBubble();
    try {
      const controller = new AbortController();
      activeStream = { planId: activePlanId, controller };

      const res = await fetch(`/api/dashboard/plan/${activePlanId}/interview`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ message }),
        signal: controller.signal,
      });

      if (!res.ok) {
        throw new Error(`${res.status} ${res.statusText}`);
      }

      setUIState('STREAMING');

      const plan = await res.json();
      const fullText = plan.planner_reply || '';

      if (currentPlan) {
        Object.assign(currentPlan, plan);
        renderPipeline();
      }

      assistantEl.classList.remove('plans-msg-streaming');
      assistantEl.querySelector('.plans-msg-content').innerHTML = simpleMarkdown(fullText);
      if (!userScrolledUp) scrollToBottom();

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
  function simpleMarkdown(text) {
    if (!text) return '';

    let html = App.escapeHtml(text);

    // Code blocks (```...```).
    html = html.replace(/```(\w*)\n([\s\S]*?)```/g, (_, lang, code) => {
      return `<pre><code>${code.trim()}</code></pre>`;
    });

    // Inline code.
    html = html.replace(/`([^`]+)`/g, '<code>$1</code>');

    // Headers.
    html = html.replace(/^### (.+)$/gm, '<h3>$1</h3>');
    html = html.replace(/^## (.+)$/gm, '<h2>$1</h2>');
    html = html.replace(/^# (.+)$/gm, '<h1>$1</h1>');

    // Bold / italic.
    html = html.replace(/\*\*(.+?)\*\*/g, '<strong>$1</strong>');
    html = html.replace(/\*(.+?)\*/g, '<em>$1</em>');

    // Checkbox lists (must come before unordered lists).
    html = html.replace(/^- \[x\] (.+)$/gm, '<li>\u2611 $1</li>');
    html = html.replace(/^- \[ \] (.+)$/gm, '<li>\u2610 $1</li>');

    // Unordered lists.
    html = html.replace(/^- (.+)$/gm, '<li>$1</li>');
    html = html.replace(/(<li>[\s\S]*?<\/li>)/g, (match) => {
      if (!match.startsWith('<ul>')) return `<ul>${match}</ul>`;
      return match;
    });
    html = html.replace(/<\/ul>\s*<ul>/g, '');

    // Ordered lists.
    html = html.replace(/^\d+\. (.+)$/gm, '<li>$1</li>');

    // Tables.
    html = html.replace(/^\|(.+)\|$/gm, (_, row) => {
      const cells = row.split('|').map(c => c.trim());
      if (cells.every(c => /^[-:]+$/.test(c))) return '';
      return '<tr>' + cells.map(c => `<td>${c}</td>`).join('') + '</tr>';
    });
    html = html.replace(/(<tr>[\s\S]*?<\/tr>)/g, (match) => {
      if (!match.startsWith('<table>')) return `<table>${match}</table>`;
      return match;
    });
    html = html.replace(/<\/table>\s*<table>/g, '');

    // Paragraphs.
    html = html.replace(/\n\n+/g, '</p><p>');
    html = html.replace(/(?<!<\/pre>)\n(?!<)/g, '<br>');

    if (!html.startsWith('<')) html = '<p>' + html + '</p>';

    return html;
  }

  // --- Refresh ---
  function refresh(project) {
    if (uiState !== 'IDLE') return;
    loadPlanList(project);
  }

  App.registerView('plan', { render, refresh });
})();
