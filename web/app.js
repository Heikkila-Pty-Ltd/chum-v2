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
    jarvisActions: ()     => API.get('/api/dashboard/jarvis/actions'),
    jarvisResolve: (body) => API.post('/api/dashboard/jarvis/actions/resolve', body),
    jarvisSummary: ()     => API.get('/api/dashboard/jarvis/summary'),
    jarvisGoals: ()       => API.get('/api/dashboard/jarvis/goals'),
    jarvisFacts: (c)      => API.get(`/api/dashboard/jarvis/facts${c ? '?category=' + c : ''}`),
    jarvisInitiatives: () => API.get('/api/dashboard/jarvis/initiatives'),
    jarvisState: ()       => API.get('/api/dashboard/jarvis/state'),
    plans: (p)            => API.get(`/api/dashboard/plans/${p}`),
    plan: (id)            => API.get(`/api/dashboard/plan/${id}`),
    planCreate: (body)    => API.post('/api/dashboard/plans', body),
    planDecompose: (id)   => API.post(`/api/dashboard/plan/${id}/decompose`, {}),
    planApprove: (id)     => API.post(`/api/dashboard/plan/${id}/approve`, {}),
    planMaterialize: (id) => API.post(`/api/dashboard/plan/${id}/materialize`, {}),
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

  // --- Shared Constants ---
  const ATTENTION_STATUSES = ['failed', 'dod_failed', 'rejected', 'needs_refinement', 'needs_review'];
  const FAILED_STATUSES = ['failed', 'dod_failed', 'rejected'];

  const STATUS_KANBAN_MAP = {
    open: 'backlog', decomposed: 'backlog',
    ready: 'ready',
    running: 'running',
    needs_review: 'review', needs_refinement: 'review', dod_failed: 'review',
    completed: 'done', done: 'done',
    failed: null, rejected: null, stale: null, // stay in current column with indicator
  };

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

  function healthColor(health) {
    return health === 'failing' ? 'var(--status-failed)' :
           health === 'degraded' ? 'var(--status-dod-failed)' :
           'var(--status-completed)';
  }

  function renderStatusBar(byStatus, total) {
    const statusOrder = ['completed', 'done', 'running', 'ready', 'open', 'decomposed',
      'needs_refinement', 'needs_review', 'dod_failed', 'failed', 'rejected', 'stale'];
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

  function bindActionButton(container, selector, apiFn, project, refreshFn) {
    container.querySelectorAll(selector).forEach(btn => {
      btn.addEventListener('click', (e) => {
        e.stopPropagation();
        const taskId = btn.dataset.taskId || btn.dataset.pause || btn.dataset.kill ||
                       btn.dataset.decompose || btn.dataset.retry || btn.dataset.suggest;
        btn.textContent = '\u2026';
        btn.disabled = true;
        apiFn(taskId).then(data => {
          btn.textContent = 'sent';
          if (data && data.suggestion) {
            const childEl = btn.closest('.ov-child');
            if (childEl) {
              let existing = childEl.nextElementSibling;
              if (existing && existing.classList.contains('ov-suggest-inline')) existing.remove();
              const div = document.createElement('div');
              div.className = 'ov-suggest-inline';
              div.textContent = data.suggestion;
              childEl.after(div);
            }
            btn.textContent = 'suggest';
            btn.disabled = false;
            return;
          }
          setTimeout(() => refreshFn(project), 1000);
        }).catch(err => {
          btn.textContent = 'err';
          btn.title = err.message;
          btn.disabled = false;
        });
      });
    });
  }

  function renderTextList(items) {
    if (!items || items.length === 0) return '';
    return `<ul class="panel-dep-list">${items.map(item => `<li>${escapeHtml(item)}</li>`).join('')}</ul>`;
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

    // PR / review link from error_log
    if (t.error_log) {
      try {
        const errInfo = JSON.parse(t.error_log);
        if (errInfo.review_url || errInfo.pr_number) {
          const prUrl = errInfo.review_url || `https://github.com/pulls/${errInfo.pr_number}`;
          const label = errInfo.pr_number ? `PR #${errInfo.pr_number}` : 'Review';
          const reason = errInfo.sub_reason ? ` \u2014 ${errInfo.sub_reason.replace(/_/g, ' ')}` : '';
          html += `
            <div class="panel-section">
              <div class="panel-section-label">Pull Request</div>
              <a class="panel-pr-link" href="${escapeHtml(prUrl)}" target="_blank" rel="noopener">${escapeHtml(label)}</a>${reason ? `<span class="panel-pr-reason">${escapeHtml(reason)}</span>` : ''}
            </div>
          `;
        }
      } catch (_) {
        if (t.error_log.trim()) {
          html += `
            <div class="panel-section">
              <div class="panel-section-label">Error</div>
              <div class="panel-description">${escapeHtml(t.error_log)}</div>
            </div>
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

      if (e.key === '1') navigate('overview');
      else if (e.key === '2') navigate('structure');
      else if (e.key === '3') navigate('jarvis');
      else if (e.key === '4') navigate('plans');
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
    healthColor,
    renderStatusBar,
    errorState,
    bindActionButton,
    ATTENTION_STATUSES,
    FAILED_STATUSES,
    STATUS_KANBAN_MAP,
    get currentProject() { return currentProject; },
  };
})();
