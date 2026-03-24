/* ============================================================
   Overview — Health metrics + attention items + per-project bars
   ============================================================ */
(() => {
  let currentData = null;

  // --- Data loading ---
  async function loadAll(project) {
    // Health is global; overview-grouped is per-project.
    // Fetch all projects list so we can show per-project bars.
    const [health, grouped, projectsRes] = await Promise.all([
      App.API.health().catch(() => null),
      App.API.overviewGrouped(project),
      App.API.projects(),
    ]);

    // For per-project bars: fetch overview-grouped for each project
    const projects = (projectsRes.projects || []);
    let projectBars = [];
    if (projects.length > 1) {
      const results = await Promise.allSettled(
        projects.map(p => App.API.overviewGrouped(p).then(d => ({ project: p, ...d })))
      );
      projectBars = results
        .filter(r => r.status === 'fulfilled')
        .map(r => r.value);
    } else if (projects.length === 1) {
      projectBars = [{ project: projects[0], ...grouped }];
    }

    return { health, grouped, projectBars, currentProject: project };
  }

  // --- Render entry ---
  function render(viewport, project) {
    viewport.innerHTML = '<div class="loading-state">loading\u2026</div>';
    loadAll(project).then(data => {
      currentData = data;
      viewport.innerHTML = renderPage(data);
      bindInteractions(viewport, project);
    }).catch(err => {
      viewport.innerHTML = App.errorState('overview', err);
    });
  }

  function renderPage(data) {
    return `<div class="view-enter overview">
      ${renderHealthStrip(data.health)}
      ${renderAttentionList(data.grouped)}
      ${renderProjectBars(data.projectBars)}
    </div>`;
  }

  // --- Health Metrics Strip ---
  function renderHealthStrip(health) {
    if (!health) {
      return `<div class="ov-health-strip">
        <div class="ov-health-card"><span class="ov-health-value">&mdash;</span><span class="ov-health-label">Health unavailable</span></div>
      </div>`;
    }

    const cards = [
      { label: 'Burn Rate (24h)', value: '$' + formatCost(health.BurnRate), warn: health.BurnRate > 10 },
      { label: 'Cost / Success', value: '$' + formatCost(health.CostPerSuccessfulTask) },
      { label: 'Quarantines', value: health.QuarantineCount || 0, warn: health.QuarantineCount > 0 },
      { label: 'Lessons', value: health.LessonCount || 0 },
      { label: 'Completed (24h)', value: (health.TaskStatusCounts ? countCompleted(health.TaskStatusCounts) : '—') },
    ];

    return `<div class="ov-health-strip">
      ${cards.map(c => `<div class="ov-health-card ${c.warn ? 'ov-health-warn' : ''}">
        <span class="ov-health-value">${c.value}</span>
        <span class="ov-health-label">${c.label}</span>
      </div>`).join('')}
    </div>`;
  }

  function formatCost(v) {
    if (v === null || v === undefined) return '0.00';
    return Number(v).toFixed(2);
  }

  function countCompleted(statusCounts) {
    return (statusCounts.completed || 0) + (statusCounts.done || 0);
  }

  // --- Attention List ---
  function renderAttentionList(grouped) {
    // Collect attention tasks from goals + orphans
    const tasks = [];

    (grouped.goals || []).forEach(g => {
      (g.children || []).forEach(c => {
        if (App.ATTENTION_STATUSES.includes(c.status)) {
          tasks.push(c);
        }
      });
      // The goal task itself might be in attention status
      if (App.ATTENTION_STATUSES.includes(g.task.status)) {
        tasks.push(g.task);
      }
    });

    (grouped.orphans || []).forEach(o => {
      if (App.ATTENTION_STATUSES.includes(o.status)) {
        tasks.push(o);
      }
    });

    // Sort by severity: quarantined > budget_exceeded > failed > dod_failed > rest
    const priority = { quarantined: 0, budget_exceeded: 1, failed: 2, dod_failed: 3, rejected: 4, needs_review: 5, needs_refinement: 6 };
    tasks.sort((a, b) => (priority[a.status] ?? 99) - (priority[b.status] ?? 99));

    if (tasks.length === 0) {
      return `<div class="ov-attention">
        <div class="ov-attention-header">Attention</div>
        <div class="ov-attention-clear">No tasks need attention</div>
      </div>`;
    }

    return `<div class="ov-attention">
      <div class="ov-attention-header">Attention <span class="ov-attention-count">${tasks.length}</span></div>
      ${tasks.map(t => {
        const retryable = ['quarantined', 'budget_exceeded', 'failed', 'dod_failed', 'rejected', 'needs_refinement'].includes(t.status);
        return `<div class="ov-attention-row" data-task-id="${App.escapeHtml(t.id)}">
          <span class="ov-attention-status">${App.statusBadge(t.status)}</span>
          <span class="ov-attention-title">${App.escapeHtml(t.title || t.id)}</span>
          <span class="ov-attention-age">${App.timeAgo(t.updated_at)}</span>
          ${retryable ? `<button class="ov-action-btn" data-retry="${App.escapeHtml(t.id)}">retry</button>` : ''}
        </div>`;
      }).join('')}
    </div>`;
  }

  // --- Per-Project Status Bars ---
  function renderProjectBars(projectBars) {
    if (!projectBars || projectBars.length === 0) {
      return '';
    }

    return `<div class="ov-project-bars">
      <div class="ov-project-bars-header">Projects</div>
      ${projectBars.map(p => {
        const byStatus = p.by_status || {};
        const total = p.total || 0;
        return `<div class="ov-project-bar-row">
          <span class="ov-project-bar-name">${App.escapeHtml(p.project)}</span>
          <span class="ov-project-bar-count">${total}</span>
          <div class="ov-project-bar-container">
            ${App.renderStatusBar(byStatus, total)}
          </div>
        </div>`;
      }).join('')}
    </div>`;
  }

  // --- Interactions ---
  function bindInteractions(viewport, project) {
    // Attention row click -> detail panel
    viewport.querySelectorAll('.ov-attention-row').forEach(row => {
      row.addEventListener('click', (e) => {
        if (e.target.closest('.ov-action-btn')) return;
        App.openPanel(row.dataset.taskId);
      });
    });

    // Retry buttons
    viewport.querySelectorAll('[data-retry]').forEach(btn => {
      btn.addEventListener('click', (e) => {
        e.stopPropagation();
        btn.textContent = '\u2026';
        btn.disabled = true;
        App.API.retry(btn.dataset.retry).then(() => {
          btn.textContent = 'sent';
          setTimeout(() => refresh(project), 1000);
        }).catch(err => {
          btn.textContent = 'err';
          btn.title = err.message;
          btn.disabled = false;
        });
      });
    });
  }

  // --- Refresh ---
  function refresh(project) {
    const viewport = document.getElementById('viewport');
    if (!viewport) return;
    const scrollTop = viewport.scrollTop;
    loadAll(project).then(data => {
      currentData = data;
      viewport.innerHTML = renderPage(data);
      bindInteractions(viewport, project);
      viewport.scrollTop = scrollTop;
    }).catch(() => {});
  }

  App.registerView('overview', { render, refresh });
})();
