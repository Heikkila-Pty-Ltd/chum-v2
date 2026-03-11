/* ============================================================
   CHUM v2 Dashboard — Core Application
   ============================================================ */

const App = (() => {
  let currentProject = '';
  let currentView = '';
  let refreshTimer = null;
  const REFRESH_INTERVAL = 30000;

  // --- API Client ---
  const API = {
    async get(path) {
      const res = await fetch(path);
      if (!res.ok) throw new Error(`${res.status} ${res.statusText}`);
      return res.json();
    },
    async post(path, body) {
      const res = await fetch(path, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(body),
      });
      if (!res.ok) throw new Error(`${res.status} ${res.statusText}`);
      return res.json();
    },
    projects: ()         => API.get('/api/dashboard/projects'),
    graph: (p)           => API.get(`/api/dashboard/graph/${p}`),
    tasks: (p, s)        => API.get(`/api/dashboard/tasks/${p}${s ? '?status=' + s : ''}`),
    task: (id)           => API.get(`/api/dashboard/task/${id}`),
    planning: (id)       => API.get(`/api/dashboard/planning/${id}`),
    planningStart: (body) => API.post('/api/dashboard/planning/start', body),
    planningSignal: (sessionId, body) => API.post(`/api/dashboard/planning/${sessionId}/signal`, body),
    stats: (p)           => API.get(`/api/dashboard/stats/${p}`),
    timeline: (p)        => API.get(`/api/dashboard/timeline/${p}`),
    overviewGrouped: (p) => API.get(`/api/dashboard/overview-grouped/${p}`),
    retry: (taskId) => API.post(`/api/dashboard/task/${taskId}/retry`),
    jarvisActions: ()    => API.get('/api/dashboard/jarvis/actions'),
    jarvisResolve: (body) => API.post('/api/dashboard/jarvis/actions/resolve', body),
    jarvisSummary: ()    => API.get('/api/dashboard/jarvis/summary'),
    jarvisGoals: ()      => API.get('/api/dashboard/jarvis/goals'),
    jarvisFacts: (c)     => API.get(`/api/dashboard/jarvis/facts${c ? '?category=' + c : ''}`),
    jarvisInitiatives: () => API.get('/api/dashboard/jarvis/initiatives'),
    jarvisState: ()      => API.get('/api/dashboard/jarvis/state'),
  };

  // --- Status Colors (read from CSS custom properties) ---
  const STATUS_NAMES = [
    'completed', 'running', 'ready', 'open', 'failed', 'decomposed',
    'dod_failed', 'needs_refinement', 'stale', 'needs_review', 'rejected', 'done',
  ];
  const STATUS_COLORS = {};
  (() => {
    const styles = getComputedStyle(document.documentElement);
    const FALLBACK = { completed:'#3d9a5f', running:'#c75a3a', ready:'#4a7fd4', open:'#5c5f69',
      failed:'#b93a3a', decomposed:'#8b6cc1', dod_failed:'#c27a2a', needs_refinement:'#b5a030',
      stale:'#4a4d56', needs_review:'#d4953a', rejected:'#9e3a5c', done:'#3d9a5f' };
    STATUS_NAMES.forEach(name => {
      const cssName = '--status-' + name.replace(/_/g, '-');
      const val = styles.getPropertyValue(cssName).trim();
      STATUS_COLORS[name] = val || FALLBACK[name] || '#5c5f69';
    });
  })();

  function statusColor(status) {
    return STATUS_COLORS[status] || '#5c5f69';
  }

  function statusDot(status) {
    return `<span class="status-dot" style="background:${statusColor(status)}"></span>`;
  }

  function statusBadge(status) {
    return `<span class="status-badge">${statusDot(status)}${status}</span>`;
  }

  // --- Utilities ---
  function formatDuration(seconds) {
    if (!seconds) return '\u2014';
    if (seconds < 60) return `${seconds}s`;
    if (seconds < 3600) return `${Math.floor(seconds / 60)}m ${seconds % 60}s`;
    const h = Math.floor(seconds / 3600);
    const m = Math.floor((seconds % 3600) / 60);
    return `${h}h ${m}m`;
  }

  function formatMinutes(mins) {
    if (!mins) return '\u2014';
    if (mins < 60) return `${mins}m`;
    return `${Math.floor(mins / 60)}h ${mins % 60}m`;
  }

  function timeAgo(dateStr) {
    if (!dateStr) return '\u2014';
    const d = new Date(dateStr);
    const now = new Date();
    const diffS = Math.floor((now - d) / 1000);
    if (diffS < 60) return 'just now';
    if (diffS < 3600) return `${Math.floor(diffS / 60)}m ago`;
    if (diffS < 86400) return `${Math.floor(diffS / 3600)}h ago`;
    return `${Math.floor(diffS / 86400)}d ago`;
  }

  function escapeHtml(s) {
    if (!s) return '';
    return s.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;').replace(/"/g, '&quot;');
  }

  function truncate(s, n) {
    if (!s) return '';
    return s.length > n ? s.slice(0, n) + '\u2026' : s;
  }

  function renderTextList(items) {
    if (!items || items.length === 0) return '';
    return `<ul class="panel-dep-list">${items.map(item => `<li>${escapeHtml(item)}</li>`).join('')}</ul>`;
  }

  function renderPlanningControls(task, planning) {
    const sessionId = planning && (planning.workflow_id || planning.session_id) ? (planning.workflow_id || planning.session_id) : '';
    return `
      <div class="panel-section planning-console" data-task-id="${escapeHtml(task.id)}" data-project="${escapeHtml(task.project || '')}" data-session-id="${escapeHtml(sessionId)}">
        <div class="panel-section-label">Planning Console</div>
        <div class="planning-console-row">
          <input class="planning-input" data-plan-agent type="text" placeholder="agent (optional)" />
          <button class="planning-button" data-plan-start type="button">${sessionId ? 'Restart' : 'Start'} planning</button>
        </div>
        <div class="planning-console-row">
          <input class="planning-input" data-plan-select-value type="text" placeholder="approach id" />
          <button class="planning-button" data-plan-action="select" type="button">Select</button>
          <button class="planning-button" data-plan-action="go" type="button">Go</button>
          <button class="planning-button" data-plan-action="approve" type="button">Approve</button>
        </div>
        <div class="planning-console-row">
          <input class="planning-input" data-plan-dig-value type="text" placeholder="approach id" />
          <input class="planning-input" data-plan-dig-reason type="text" placeholder="dig feedback" />
          <button class="planning-button" data-plan-action="dig" type="button">Dig</button>
        </div>
        <div class="planning-console-row">
          <input class="planning-input" data-plan-answer type="text" placeholder="ask a question or answer a prompt" />
          <button class="planning-button" data-plan-action="answer" type="button">Send</button>
        </div>
        <div class="planning-console-row">
          <button class="planning-button planning-button-subtle" data-plan-action="realign" type="button">Realign</button>
          <button class="planning-button planning-button-subtle" data-plan-action="stop" type="button">Stop</button>
        </div>
        <div class="planning-console-feedback" data-plan-feedback>Use this panel to drive the ceremony without Matrix.</div>
      </div>
    `;
  }

  function renderPlanningSection(task, planning, sessions) {
    const currentPlanning = planning || {};
    const history = currentPlanning.history || [];
    const spec = currentPlanning.plan_spec || null;
    const selected = currentPlanning.selected_approach || null;
    const sessionCount = sessions && sessions.length ? sessions.length : (planning && planning.session_id ? 1 : 0);

    let html = `
      <div class="panel-section">
        <div class="panel-section-label">Planning${sessionCount ? ` (${sessionCount} session${sessionCount === 1 ? '' : 's'})` : ''}</div>
        <div class="panel-description">
          ${planning && planning.session_id
            ? `<strong>${escapeHtml(currentPlanning.phase || 'unknown')}</strong> · ${escapeHtml(currentPlanning.status || 'unknown')}
               ${currentPlanning.workflow_status ? ` · workflow ${escapeHtml(currentPlanning.workflow_status)}` : ''}
               ${currentPlanning.updated_at ? ` · updated ${escapeHtml(timeAgo(currentPlanning.updated_at))}` : ''}`
            : 'No planning session recorded yet.'}
        </div>
      </div>
    `;

    html += renderPlanningControls(task, currentPlanning);

    if (currentPlanning.goal && currentPlanning.goal.intent) {
      html += `
        <div class="panel-section">
          <div class="panel-section-label">Clarified Goal</div>
          <div class="panel-description">${escapeHtml(currentPlanning.goal.intent)}</div>
          ${currentPlanning.goal.why ? `<div class="panel-description" style="margin-top:6px;color:var(--text-secondary)">Why: ${escapeHtml(currentPlanning.goal.why)}</div>` : ''}
        </div>
      `;
    }

    if (selected) {
      html += `
        <div class="panel-section">
          <div class="panel-section-label">Chosen Approach</div>
          <div class="panel-description"><strong>${escapeHtml(selected.title)}</strong></div>
          <div class="panel-description">${escapeHtml(selected.description || '')}</div>
          ${selected.tradeoffs ? `<div class="panel-description" style="margin-top:6px;color:var(--text-secondary)">Tradeoffs: ${escapeHtml(selected.tradeoffs)}</div>` : ''}
        </div>
      `;
    }

    if (spec) {
      html += `
        <div class="panel-section">
          <div class="panel-section-label">Plan Contract</div>
          <div class="panel-description">${escapeHtml(spec.summary || '')}</div>
          ${spec.expected_pr_outcome ? `<div class="panel-description" style="margin-top:6px;color:var(--text-secondary)">PR outcome: ${escapeHtml(spec.expected_pr_outcome)}</div>` : ''}
        </div>
      `;

      if (spec.validation_strategy && spec.validation_strategy.length > 0) {
        html += `
          <div class="panel-section">
            <div class="panel-section-label">Validation Strategy</div>
            ${renderTextList(spec.validation_strategy)}
          </div>
        `;
      }

      if (spec.risks && spec.risks.length > 0) {
        html += `
          <div class="panel-section">
            <div class="panel-section-label">Risks</div>
            ${renderTextList(spec.risks)}
          </div>
        `;
      }
    }

    if (history.length > 0) {
      html += `
        <div class="panel-section">
          <div class="panel-section-label">Phase History</div>
          ${history.map(entry => `
            <div style="font-size:11px;color:var(--text-secondary);padding:2px 0">
              <strong>${escapeHtml(entry.phase)}</strong> · ${escapeHtml(entry.status)}
              ${entry.note ? ` · ${escapeHtml(entry.note)}` : ''}
            </div>
          `).join('')}
        </div>
      `;
    }

    return html;
  }

  // --- Router ---
  const views = {};

  function registerView(name, mod) {
    views[name] = mod;
  }

  function navigate(view, project) {
    if (project) currentProject = project;
    currentView = view;

    // Update nav
    document.querySelectorAll('.nav-link').forEach(a => {
      a.classList.toggle('active', a.dataset.view === view);
    });

    // Render
    const viewport = document.getElementById('viewport');
    viewport.innerHTML = '<div class="loading-state">loading\u2026</div>';
    closePanel();

    if (views[view]) {
      views[view].render(viewport, currentProject);
    }

    // Restart refresh timer
    startRefresh();
  }

  function parseHash() {
    const hash = location.hash.slice(1) || '/overview';
    const parts = hash.split('/').filter(Boolean);
    return { view: parts[0] || 'overview', param: parts[1] || '' };
  }

  // --- Detail Panel ---
  function openPanel(taskId) {
    const panel = document.getElementById('detail-panel');
    const content = document.getElementById('panel-content');
    content.innerHTML = '<div class="loading-state">loading\u2026</div>';
    panel.classList.add('panel-open');
    panel.setAttribute('aria-hidden', 'false');

    API.task(taskId).then(data => {
      content.innerHTML = renderTaskDetail(data);

      // Bind dep links
      content.querySelectorAll('.panel-dep-link').forEach(a => {
        a.addEventListener('click', (e) => {
          e.preventDefault();
          openPanel(a.dataset.taskId);
        });
      });

      bindPlanningControls(content, taskId, data);
    }).catch(err => {
      content.innerHTML = `<div class="empty-state">Failed to load task<div class="empty-state-hint">${escapeHtml(err.message)}</div></div>`;
    });
  }

  function closePanel() {
    const panel = document.getElementById('detail-panel');
    panel.classList.remove('panel-open');
    panel.setAttribute('aria-hidden', 'true');
  }

  function renderTaskDetail(data) {
    const t = data.task;
    let html = `
      <div class="panel-task-id">${escapeHtml(t.id)}</div>
      <div class="panel-task-title">${escapeHtml(t.title)}</div>
      <div class="panel-section">
        ${statusBadge(t.status)}
      </div>
    `;

    if (t.description) {
      html += `
        <div class="panel-section">
          <div class="panel-section-label">Description</div>
          <div class="panel-description">${escapeHtml(t.description)}</div>
        </div>
      `;
    }

    html += `
      <div class="panel-section">
        <div class="panel-section-label">Details</div>
        <div class="panel-meta-grid">
          <span class="panel-meta-key">type</span><span class="panel-meta-value">${t.type || 'task'}</span>
          <span class="panel-meta-key">priority</span><span class="panel-meta-value">${t.priority}</span>
          <span class="panel-meta-key">estimate</span><span class="panel-meta-value">${formatMinutes(t.estimate_minutes)}</span>
          <span class="panel-meta-key">actual</span><span class="panel-meta-value">${formatDuration(t.actual_duration_sec)}</span>
          <span class="panel-meta-key">iterations</span><span class="panel-meta-value">${t.iterations_used || '\u2014'}</span>
          <span class="panel-meta-key">assignee</span><span class="panel-meta-value">${t.assignee || '\u2014'}</span>
          <span class="panel-meta-key">created</span><span class="panel-meta-value">${timeAgo(t.created_at)}</span>
          <span class="panel-meta-key">updated</span><span class="panel-meta-value">${timeAgo(t.updated_at)}</span>
        </div>
      </div>
    `;

    if (t.acceptance) {
      html += `
        <div class="panel-section">
          <div class="panel-section-label">Acceptance</div>
          <div class="panel-description">${escapeHtml(t.acceptance)}</div>
        </div>
      `;
    }

    if (data.dependencies.length > 0) {
      html += `
        <div class="panel-section">
          <div class="panel-section-label">Depends On (${data.dependencies.length})</div>
          <ul class="panel-dep-list">${data.dependencies.map(id =>
            `<li><a class="panel-dep-link" data-task-id="${escapeHtml(id)}" href="#">${escapeHtml(id)}</a></li>`
          ).join('')}</ul>
        </div>
      `;
    }

    if (data.dependents.length > 0) {
      html += `
        <div class="panel-section">
          <div class="panel-section-label">Dependents (${data.dependents.length})</div>
          <ul class="panel-dep-list">${data.dependents.map(id =>
            `<li><a class="panel-dep-link" data-task-id="${escapeHtml(id)}" href="#">${escapeHtml(id)}</a></li>`
          ).join('')}</ul>
        </div>
      `;
    }

    if (data.targets.length > 0) {
      html += `
        <div class="panel-section">
          <div class="panel-section-label">Code Targets (${data.targets.length})</div>
          ${data.targets.map(tgt =>
            `<div class="panel-target">${escapeHtml(tgt.file_path)}${tgt.symbol_name ? `:<span class="panel-target-symbol">${escapeHtml(tgt.symbol_name)}</span>` : ''}</div>`
          ).join('')}
        </div>
      `;
    }

    if (data.decisions.length > 0) {
      html += `
        <div class="panel-section">
          <div class="panel-section-label">Decisions (${data.decisions.length})</div>
          ${data.decisions.map(dec => `
            <div style="margin-bottom:var(--sp-3)">
              <div style="font-size:12px;font-weight:500;margin-bottom:var(--sp-1)">${escapeHtml(dec.title)}</div>
              ${dec.alternatives.map(alt => `
                <div style="font-size:11px;color:var(--text-secondary);padding:2px 0;display:flex;gap:var(--sp-2);align-items:baseline">
                  <span style="color:${alt.selected ? 'var(--accent)' : 'var(--text-tertiary)'}">${alt.selected ? '\u25c9' : '\u25cb'}</span>
                  <span>${escapeHtml(alt.label)}</span>
                  ${alt.uct_score > 0 ? `<span style="font-family:var(--font-mono);font-size:10px;color:var(--text-tertiary)">uct:${alt.uct_score.toFixed(2)}</span>` : ''}
                </div>
              `).join('')}
            </div>
          `).join('')}
        </div>
      `;
    }

    html += renderPlanningSection(t, data.planning, data.planning_sessions);

    return html;
  }

  function setPlanningFeedback(container, message, isError) {
    const target = container.querySelector('[data-plan-feedback]');
    if (!target) return;
    target.textContent = message;
    target.style.color = isError ? 'var(--status-failed)' : 'var(--text-secondary)';
  }

  function bindPlanningControls(content, taskId, data) {
    const container = content.querySelector('.planning-console');
    if (!container) return;

    const project = container.dataset.project || data.task.project || currentProject;
    const getSessionId = () => container.dataset.sessionId || '';

    const refreshPanel = () => openPanel(taskId);

    const withAction = async (fn, pendingMessage) => {
      setPlanningFeedback(container, pendingMessage, false);
      try {
        await fn();
        setPlanningFeedback(container, 'Planning command sent. Reloading panel…', false);
        setTimeout(refreshPanel, 400);
      } catch (err) {
        setPlanningFeedback(container, err.message || 'Planning command failed', true);
      }
    };

    const startButton = container.querySelector('[data-plan-start]');
    if (startButton) {
      startButton.addEventListener('click', () => withAction(async () => {
        const agent = (container.querySelector('[data-plan-agent]')?.value || '').trim();
        const resp = await API.planningStart({
          project,
          goal_id: taskId,
          agent,
        });
        if (resp.workflow_id || resp.session_id) {
          container.dataset.sessionId = resp.workflow_id || resp.session_id;
        }
      }, 'Starting planning session…'));
    }

    container.querySelectorAll('[data-plan-action]').forEach(btn => {
      btn.addEventListener('click', () => withAction(async () => {
        const action = btn.dataset.planAction;
        let value = '';
        let reason = '';

        if (action === 'select') {
          value = (container.querySelector('[data-plan-select-value]')?.value || '').trim();
        } else if (action === 'dig') {
          value = (container.querySelector('[data-plan-dig-value]')?.value || '').trim();
          reason = (container.querySelector('[data-plan-dig-reason]')?.value || '').trim();
        } else if (action === 'answer') {
          value = (container.querySelector('[data-plan-answer]')?.value || '').trim();
        }

        const sessionId = getSessionId();
        if (!sessionId) {
          throw new Error('No planning session yet. Start one first.');
        }

        await API.planningSignal(sessionId, { action, value, reason });
      }, `Sending ${btn.dataset.planAction}…`));
    });
  }

  // --- Auto Refresh ---
  function startRefresh() {
    stopRefresh();
    const indicator = document.getElementById('live-indicator');
    indicator.className = 'indicator-live';
    indicator.querySelector('.indicator-label').textContent = 'live';

    refreshTimer = setInterval(() => {
      if (views[currentView] && views[currentView].refresh) {
        views[currentView].refresh(currentProject);
      }
    }, REFRESH_INTERVAL);
  }

  function stopRefresh() {
    if (refreshTimer) {
      clearInterval(refreshTimer);
      refreshTimer = null;
    }
    const indicator = document.getElementById('live-indicator');
    indicator.className = 'indicator-idle';
    indicator.querySelector('.indicator-label').textContent = 'idle';
  }

  // --- Keyboard Shortcuts ---
  function setupKeyboard() {
    document.addEventListener('keydown', (e) => {
      if (e.target.tagName === 'INPUT' || e.target.tagName === 'SELECT') return;

      if (e.key === '1') navigate('overview');
      else if (e.key === '2') navigate('tree');
      else if (e.key === '3') navigate('dag');
      else if (e.key === '4') navigate('tasks');
      else if (e.key === '5') navigate('timeline');
      else if (e.key === '6') navigate('stats');
      else if (e.key === '7') navigate('jarvis');
      else if (e.key === '8') navigate('plans');
      else if (e.key === 'Escape') closePanel();
    });
  }

  // --- Init ---
  async function init() {
    // Panel close button
    document.getElementById('panel-close').addEventListener('click', closePanel);

    // Keyboard shortcuts
    setupKeyboard();

    // Load projects
    try {
      const data = await API.projects();
      const projects = data.projects || [];

      if (projects.length > 0) {
        currentProject = projects[0];
        const sel = document.getElementById('project-selector');
        if (projects.length > 1) {
          const select = document.createElement('select');
          projects.forEach(p => {
            const opt = document.createElement('option');
            opt.value = p;
            opt.textContent = p;
            select.appendChild(opt);
          });
          select.addEventListener('change', () => {
            currentProject = select.value;
            navigate(currentView);
          });
          sel.appendChild(select);
        } else {
          sel.innerHTML = `<span style="font-family:var(--font-mono);font-size:12px;color:var(--text-secondary)">${escapeHtml(projects[0])}</span>`;
        }
      }
    } catch {
      // Projects endpoint not available — use empty
      currentProject = 'chum';
    }

    // Hash routing
    window.addEventListener('hashchange', () => {
      const { view } = parseHash();
      navigate(view);
    });

    // Nav link clicks
    document.querySelectorAll('.nav-link').forEach(a => {
      a.addEventListener('click', (e) => {
        e.preventDefault();
        location.hash = a.getAttribute('href');
      });
    });

    // Initial route
    const { view } = parseHash();
    navigate(view);
  }

  // Public API
  return {
    init,
    API,
    registerView,
    navigate,
    openPanel,
    closePanel,
    statusColor,
    statusDot,
    statusBadge,
    formatDuration,
    formatMinutes,
    timeAgo,
    escapeHtml,
    truncate,
    get currentProject() { return currentProject; },
  };
})();
