/* ============================================================
   Tasks View — Filterable task list
   ============================================================ */

(() => {
  const ALL_STATUSES = ['completed', 'running', 'ready', 'open', 'failed', 'decomposed', 'dod_failed', 'needs_refinement', 'stale', 'needs_review', 'rejected', 'done'];
  let activeFilters = new Set();
  let allTasks = [];

  function render(viewport, project) {
    viewport.innerHTML = `
      <div class="view-enter">
        <div class="tasks-toolbar">
          <div class="tasks-filter-group" id="task-filters"></div>
          <div class="tasks-count" id="task-count"></div>
        </div>
        <div id="task-table-wrap"></div>
      </div>
    `;

    activeFilters.clear();
    loadTasks(project);
  }

  async function loadTasks(project) {
    try {
      const data = await App.API.tasks(project);
      allTasks = data.tasks || [];
      buildFilters();
      renderTable();
    } catch (err) {
      document.getElementById('task-table-wrap').innerHTML =
        `<div class="empty-state">Failed to load tasks<div class="empty-state-hint">${App.escapeHtml(err.message)}</div></div>`;
    }
  }

  function buildFilters() {
    const counts = {};
    allTasks.forEach(t => { counts[t.status] = (counts[t.status] || 0) + 1; });

    const container = document.getElementById('task-filters');
    if (!container) return;
    container.innerHTML = '';

    // "All" button
    const allBtn = document.createElement('button');
    allBtn.className = 'filter-btn active';
    allBtn.textContent = `All (${allTasks.length})`;
    allBtn.addEventListener('click', () => {
      activeFilters.clear();
      updateFilterUI();
      renderTable();
    });
    container.appendChild(allBtn);

    // Status buttons (only for statuses that exist)
    ALL_STATUSES.forEach(status => {
      if (!counts[status]) return;
      const btn = document.createElement('button');
      btn.className = 'filter-btn';
      btn.textContent = `${status} (${counts[status]})`;
      btn.dataset.status = status;
      btn.addEventListener('click', () => {
        if (activeFilters.has(status)) {
          activeFilters.delete(status);
        } else {
          activeFilters.add(status);
        }
        updateFilterUI();
        renderTable();
      });
      container.appendChild(btn);
    });
  }

  function updateFilterUI() {
    const btns = document.querySelectorAll('#task-filters .filter-btn');
    btns.forEach(btn => {
      if (!btn.dataset.status) {
        // "All" button
        btn.classList.toggle('active', activeFilters.size === 0);
      } else {
        btn.classList.toggle('active', activeFilters.has(btn.dataset.status));
      }
    });
  }

  function renderTable() {
    const filtered = activeFilters.size === 0
      ? allTasks
      : allTasks.filter(t => activeFilters.has(t.status));

    const countEl = document.getElementById('task-count');
    if (countEl) countEl.textContent = `${filtered.length} task${filtered.length !== 1 ? 's' : ''}`;

    const wrap = document.getElementById('task-table-wrap');
    if (!wrap) return;

    if (filtered.length === 0) {
      wrap.innerHTML = '<div class="empty-state">No tasks match the current filter</div>';
      return;
    }

    wrap.innerHTML = `
      <table class="task-table">
        <thead>
          <tr>
            <th style="width:36px"></th>
            <th>ID</th>
            <th>Title</th>
            <th>Priority</th>
            <th>Estimate</th>
            <th>Actual</th>
            <th>Updated</th>
          </tr>
        </thead>
        <tbody>
          ${filtered.map(t => `
            <tr data-task-id="${App.escapeHtml(t.id)}">
              <td>${App.statusDot(t.status)}</td>
              <td class="col-id">${App.escapeHtml(t.id)}</td>
              <td class="col-title">${App.escapeHtml(t.title)}</td>
              <td class="col-priority">${t.priority}</td>
              <td class="col-estimate">${App.formatMinutes(t.estimate_minutes)}</td>
              <td class="col-duration">${App.formatDuration(t.actual_duration_sec)}</td>
              <td class="col-updated">${App.timeAgo(t.updated_at)}</td>
            </tr>
          `).join('')}
        </tbody>
      </table>
    `;

    // Row clicks
    wrap.querySelectorAll('tbody tr').forEach(row => {
      row.addEventListener('click', () => {
        App.openPanel(row.dataset.taskId);
      });
    });
  }

  function refresh(project) {
    loadTasks(project);
  }

  App.registerView('tasks', { render, refresh });
})();
