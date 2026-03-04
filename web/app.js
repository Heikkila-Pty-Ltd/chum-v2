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
    stats: (p)           => API.get(`/api/dashboard/stats/${p}`),
    timeline: (p)        => API.get(`/api/dashboard/timeline/${p}`),
    overviewGrouped: (p) => API.get(`/api/dashboard/overview-grouped/${p}`),
    retry: (taskId) => API.post(`/api/dashboard/task/${taskId}/retry`),
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
      if (e.target.tagName === 'INPUT' || e.target.tagName === 'SELECT') return;

      if (e.key === '1') navigate('overview');
      else if (e.key === '2') navigate('tree');
      else if (e.key === '3') navigate('dag');
      else if (e.key === '4') navigate('tasks');
      else if (e.key === '5') navigate('timeline');
      else if (e.key === '6') navigate('stats');
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
