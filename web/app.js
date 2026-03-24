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
    projects: ()          => API.get('/api/dashboard/projects'),
    graph: (p)            => API.get(`/api/dashboard/graph/${p}`),
    tree: (p)             => API.get(`/api/dashboard/tree/${p}`),
    tasks: (p, s)         => API.get(`/api/dashboard/tasks/${p}${s ? '?status=' + s : ''}`),
    task: (id)            => API.get(`/api/dashboard/task/${id}`),
    overviewGrouped: (p)  => API.get(`/api/dashboard/overview-grouped/${p}`),
    retry: (taskId)       => API.post(`/api/dashboard/task/${taskId}/retry`),
    pause: (taskId)       => API.post(`/api/dashboard/task/${taskId}/pause`),
    kill: (taskId, reason) => API.post(`/api/dashboard/task/${taskId}/kill`, { reason }),
    decompose: (taskId)   => API.post(`/api/dashboard/task/${taskId}/decompose`),
    suggest: (taskId)     => API.get(`/api/dashboard/suggest/${taskId}`),
    health: ()            => API.get('/api/dashboard/health'),
    traces: (id)          => API.get(`/api/dashboard/traces/${id}`),
    plans: (p)            => API.get(`/api/dashboard/plans/${p}`),
    plan: (id)            => API.get(`/api/dashboard/plan/${id}`),
    planCreate: (body)    => API.post('/api/dashboard/plans', body),
    planDecompose: (id)   => API.post(`/api/dashboard/plan/${id}/decompose`, {}),
    planApprove: (id)     => API.post(`/api/dashboard/plan/${id}/approve`, {}),
    planMaterialize: (id) => API.post(`/api/dashboard/plan/${id}/materialize`, {}),
    // New dashboard endpoints
    overview: (p)         => API.get(`/api/dashboard/overview/${p}`),
    stats: (p)            => API.get(`/api/dashboard/stats/${p}`),
    timeline: (p)         => API.get(`/api/dashboard/timeline/${p}`),
    lessons: (p)          => API.get(`/api/dashboard/lessons/${p}`),
    activity: (hours, project) => {
      let url = `/api/dashboard/activity?hours=${hours || 24}`;
      if (project) url += `&project=${encodeURIComponent(project)}`;
      return API.get(url);
    },
    projectPause: (name) => API.post(`/api/dashboard/project/${name}/pause`),
    projectResume: (name) => API.post(`/api/dashboard/project/${name}/resume`),
    queueReorder: (ids) => API.post('/api/dashboard/queue/reorder', { task_ids: ids }),
    learningTrends: () => API.get('/api/dashboard/learning/trends'),
    modelPerf: () => API.get('/api/dashboard/learning/model-perf'),
  };

  // --- Status Colors (read from CSS custom properties) ---
  const STATUS_NAMES = [
    'completed', 'running', 'ready', 'open', 'failed', 'decomposed',
    'dod_failed', 'needs_refinement', 'stale', 'needs_review', 'rejected', 'done',
    'quarantined', 'budget_exceeded', 'paused',
  ];
  const STATUS_COLORS = {};
  (() => {
    const styles = getComputedStyle(document.documentElement);
    const FALLBACK = { completed:'#3d9a5f', running:'#c75a3a', ready:'#4a7fd4', open:'#5c5f69',
      failed:'#b93a3a', decomposed:'#8b6cc1', dod_failed:'#c27a2a', needs_refinement:'#b5a030',
      stale:'#4a4d56', needs_review:'#d4953a', rejected:'#9e3a5c', done:'#3d9a5f',
      quarantined:'#8b4a8b', budget_exceeded:'#c9843a', paused:'#6b7280' };
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

  // --- Shared Constants ---
  const ATTENTION_STATUSES = ['quarantined', 'budget_exceeded', 'failed', 'dod_failed', 'rejected', 'needs_refinement', 'needs_review'];

  // --- Shared Utilities ---
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

  function renderStatusBar(byStatus, total) {
    const statusOrder = ['completed', 'done', 'running', 'ready', 'open', 'decomposed',
      'needs_refinement', 'needs_review', 'dod_failed', 'failed', 'rejected', 'quarantined', 'budget_exceeded', 'stale'];
    const segments = statusOrder
      .filter(s => byStatus[s] > 0)
      .map(s => ({ status: s, count: byStatus[s], pct: total > 0 ? (byStatus[s] / total) * 100 : 0 }));

    return `<div class="ov-status-bar">
      ${segments.map(s =>
        `<div class="status-bar-segment" style="width:${Math.max(s.pct, 1)}%;background:${statusColor(s.status)}" title="${s.status}: ${s.count}"></div>`
      ).join('')}
    </div>`;
  }

  function errorState(label, err) {
    return `<div class="empty-state">Failed to load ${escapeHtml(label)}<div class="empty-state-hint">${escapeHtml(err.message)}</div></div>`;
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

    // Destroy any Alpine.js trees before clearing DOM to prevent leaked state.
    const viewport = document.getElementById('viewport');
    if (window.Alpine && viewport.children.length) {
      Alpine.destroyTree(viewport);
    }
    viewport.innerHTML = '<div class="loading-state">loading\u2026</div>';
    closePanel();

    if (views[view]) {
      const { param } = parseHash();
      views[view].render(viewport, currentProject, param);
    }

    // Restart refresh timer
    startRefresh();
  }

  const ROUTE_REDIRECTS = {
    overview: 'check', structure: 'work', jarvis: 'check',
    plans: 'plan', planner: 'plan', tasks: 'work', projects: 'work',
  };

  function parseHash() {
    const hash = location.hash.slice(1) || '/check';
    const parts = hash.split('/').filter(Boolean);
    let view = parts[0] || 'check';
    if (ROUTE_REDIRECTS[view]) view = ROUTE_REDIRECTS[view];
    return { view, param: parts[1] || '' };
  }

  // --- Detail Panel ---
  function openPanel(taskId) {
    const panel = document.getElementById('detail-panel');
    const content = document.getElementById('panel-content');
    content.innerHTML = '<div class="loading-state">loading\u2026</div>';
    panel.classList.add('panel-open');
    panel.setAttribute('aria-hidden', 'false');

    Promise.all([
      API.task(taskId),
      API.traces(taskId).catch(() => ({ traces: [] })),
    ]).then(([data, tracesData]) => {
      data.traces = tracesData.traces || [];
      content.innerHTML = renderTaskDetail(data);

      // Bind dep links
      content.querySelectorAll('.panel-dep-link').forEach(a => {
        a.addEventListener('click', (e) => {
          e.preventDefault();
          openPanel(a.dataset.taskId);
        });
      });
      // Bind trace toggles
      content.querySelectorAll('.panel-trace-toggle').forEach(btn => {
        btn.addEventListener('click', () => {
          const body = btn.nextElementSibling;
          if (body) {
            body.classList.toggle('panel-trace-visible');
            btn.textContent = body.classList.contains('panel-trace-visible')
              ? btn.textContent.replace('\u25b6', '\u25bc')
              : btn.textContent.replace('\u25bc', '\u25b6');
          }
        });
      });
    }).catch(err => {
      content.innerHTML = errorState('task', err);
    });
  }

  function closePanel() {
    const panel = document.getElementById('detail-panel');
    panel.classList.remove('panel-open');
    panel.setAttribute('aria-hidden', 'true');
  }

  function renderTaskDetail(data) {
    const t = data.task;

    // --- Header: id + status + PR link ---
    let prLink = '';
    if (t.error_log) {
      try {
        const errInfo = JSON.parse(t.error_log);
        if (errInfo.review_url || errInfo.pr_number) {
          const prUrl = errInfo.review_url || `https://github.com/pulls/${errInfo.pr_number}`;
          const label = errInfo.pr_number ? `PR #${errInfo.pr_number}` : 'Review';
          prLink = `<a class="panel-pr-chip" href="${escapeHtml(prUrl)}" target="_blank" rel="noopener">${escapeHtml(label)}</a>`;
        }
      } catch (_) {}
    }

    let html = `
      <div class="panel-header">
        <div class="panel-header-top">
          <span class="panel-task-id">${escapeHtml(t.id)}</span>
          ${statusBadge(t.status)}
          ${prLink}
        </div>
        <div class="panel-task-title">${escapeHtml(t.title)}</div>
      </div>
    `;

    // --- Stats: compact chips, only show non-empty values ---
    const stats = [];
    if (t.priority) stats.push(`P${t.priority}`);
    if (t.attempt_count) stats.push(`${t.attempt_count} attempt${t.attempt_count > 1 ? 's' : ''}`);
    if (t.estimate_minutes) stats.push(`est ${formatMinutes(t.estimate_minutes)}`);
    if (t.actual_duration_sec) stats.push(`took ${formatDuration(t.actual_duration_sec)}`);
    stats.push(timeAgo(t.created_at));

    html += `<div class="panel-stats">${stats.map(s => `<span class="panel-stat">${s}</span>`).join('')}</div>`;

    // --- Graph: deps + dependents inline ---
    if (data.dependencies.length > 0 || data.dependents.length > 0) {
      html += `<div class="panel-graph">`;
      if (data.dependencies.length > 0) {
        html += `<span class="panel-graph-label">\u2190</span>${data.dependencies.map(id =>
          `<a class="panel-dep-link" data-task-id="${escapeHtml(id)}" href="#">${escapeHtml(id)}</a>`
        ).join('')}`;
      }
      if (data.dependents.length > 0) {
        if (data.dependencies.length > 0) html += `<span class="panel-graph-sep"></span>`;
        html += `<span class="panel-graph-label">\u2192</span>${data.dependents.map(id =>
          `<a class="panel-dep-link" data-task-id="${escapeHtml(id)}" href="#">${escapeHtml(id)}</a>`
        ).join('')}`;
      }
      html += `</div>`;
    }

    // --- Description: collapsible ---
    if (t.description) {
      html += `
        <details class="panel-details">
          <summary class="panel-section-label">Description</summary>
          <div class="panel-description">${escapeHtml(t.description)}</div>
        </details>
      `;
    }

    // --- Acceptance: collapsible ---
    if (t.acceptance) {
      html += `
        <details class="panel-details">
          <summary class="panel-section-label">Acceptance</summary>
          <div class="panel-description">${escapeHtml(t.acceptance)}</div>
        </details>
      `;
    }

    // --- Code targets: just filename:symbol ---
    if (data.targets.length > 0) {
      html += `
        <details class="panel-details">
          <summary class="panel-section-label">Files (${data.targets.length})</summary>
          ${data.targets.map(tgt => {
            const short = tgt.file_path.split('/').slice(-2).join('/');
            return `<div class="panel-target">${escapeHtml(short)}${tgt.symbol_name ? `:<span class="panel-target-symbol">${escapeHtml(tgt.symbol_name)}</span>` : ''}</div>`;
          }).join('')}
        </details>
      `;
    }

    // --- Decisions ---
    if (data.decisions.length > 0) {
      html += `
        <details class="panel-details">
          <summary class="panel-section-label">Decisions (${data.decisions.length})</summary>
          ${data.decisions.map(dec => `
            <div class="panel-decision">
              <div class="panel-decision-title">${escapeHtml(dec.title)}</div>
              ${dec.alternatives.map(alt => `
                <div class="panel-decision-alt">
                  <span style="color:${alt.selected ? 'var(--accent)' : 'var(--text-tertiary)'}">${alt.selected ? '\u25c9' : '\u25cb'}</span>
                  <span>${escapeHtml(alt.label)}</span>
                  ${alt.uct_score > 0 ? `<span class="panel-decision-uct">uct:${alt.uct_score.toFixed(2)}</span>` : ''}
                </div>
              `).join('')}
            </div>
          `).join('')}
        </details>
      `;
    }

    // --- Execution traces ---
    if (data.traces && data.traces.length > 0) {
      html += `
        <details class="panel-details">
          <summary class="panel-section-label">Traces (${data.traces.length})</summary>
          ${data.traces.map((tr, i) => `
            <div class="panel-trace">
              <button class="panel-trace-toggle">\u25b6 #${i + 1} ${escapeHtml(tr.outcome || tr.status || '?')}${tr.duration_sec ? ' \u00b7 ' + formatDuration(tr.duration_sec) : ''}${tr.cost_usd ? ' \u00b7 $' + Number(tr.cost_usd).toFixed(4) : ''}</button>
              <div class="panel-trace-body">
                ${tr.error_snippet ? `<pre class="panel-trace-error">${escapeHtml(tr.error_snippet)}</pre>` : '<span class="panel-trace-cost">No error details</span>'}
              </div>
            </div>
          `).join('')}
        </details>
      `;
    }

    // --- Error (non-JSON error_log) ---
    if (t.error_log && !prLink) {
      try { JSON.parse(t.error_log); } catch (_) {
        if (t.error_log.trim()) {
          html += `
            <details class="panel-details" open>
              <summary class="panel-section-label">Error</summary>
              <pre class="panel-trace-error">${escapeHtml(t.error_log)}</pre>
            </details>
          `;
        }
      }
    }

    return html;
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
      if (e.target.tagName === 'INPUT' || e.target.tagName === 'SELECT' || e.target.tagName === 'TEXTAREA') return;

      if (e.key === '1') navigate('check');
      else if (e.key === '2') navigate('plan');
      else if (e.key === '3') navigate('steer');
      else if (e.key === '4') navigate('learn');
      else if (e.key === '5') navigate('work');
      else if (e.key === 'Escape') closePanel();
    });
  }

  // --- Init ---
  async function init() {
    document.getElementById('panel-close').addEventListener('click', closePanel);
    setupKeyboard();

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
      currentProject = 'chum';
    }

    window.addEventListener('hashchange', () => {
      const { view } = parseHash();
      navigate(view);
    });

    document.querySelectorAll('.nav-link').forEach(a => {
      a.addEventListener('click', (e) => {
        e.preventDefault();
        location.hash = a.getAttribute('href');
      });
    });

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
    renderStatusBar,
    errorState,
    STATUS_NAMES,
    ATTENTION_STATUSES,
    get currentProject() { return currentProject; },
  };
})();
