/* ============================================================
   Stats View — Summary metrics and status distribution
   ============================================================ */

(() => {

  function render(viewport, project) {
    viewport.innerHTML = '<div class="loading-state">loading\u2026</div>';

    App.API.stats(project).then(data => {
      viewport.innerHTML = renderStats(data);
    }).catch(err => {
      viewport.innerHTML = `<div class="empty-state">Failed to load stats<div class="empty-state-hint">${App.escapeHtml(err.message)}</div></div>`;
    });
  }

  function renderStats(data) {
    const total = data.total || 0;
    const byStatus = data.by_status || {};
    const completed = (byStatus.completed || 0) + (byStatus.done || 0);
    const running = byStatus.running || 0;
    const failed = (byStatus.failed || 0) + (byStatus.dod_failed || 0) + (byStatus.rejected || 0);
    const completionRate = total > 0 ? Math.round((completed / total) * 100) : 0;
    const totalHours = data.total_actual_seconds ? (data.total_actual_seconds / 3600).toFixed(1) : '0';
    const estHours = data.total_estimate_minutes ? (data.total_estimate_minutes / 60).toFixed(1) : '0';

    // Status bar segments ordered by visual weight
    const statusOrder = ['completed', 'done', 'running', 'ready', 'open', 'decomposed', 'needs_refinement', 'needs_review', 'dod_failed', 'failed', 'rejected', 'stale'];
    const segments = statusOrder
      .filter(s => byStatus[s] > 0)
      .map(s => ({
        status: s,
        count: byStatus[s],
        pct: total > 0 ? (byStatus[s] / total) * 100 : 0,
      }));

    return `
      <div class="view-enter">
        <div class="stats-grid">
          <div class="stat-cell">
            <div class="stat-label">Total Tasks</div>
            <div class="stat-value">${total}</div>
            <div class="stat-sub">${Object.keys(byStatus).length} statuses</div>
          </div>
          <div class="stat-cell">
            <div class="stat-label">Completion Rate</div>
            <div class="stat-value">${completionRate}<span style="font-size:16px;color:var(--text-tertiary)">%</span></div>
            <div class="stat-sub">${completed} completed of ${total}</div>
          </div>
          <div class="stat-cell">
            <div class="stat-label">Active Now</div>
            <div class="stat-value" style="color:${running > 0 ? 'var(--status-running)' : 'var(--text-primary)'}">${running}</div>
            <div class="stat-sub">${failed > 0 ? `${failed} failed` : 'no failures'}</div>
          </div>
          <div class="stat-cell">
            <div class="stat-label">Time Spent</div>
            <div class="stat-value">${totalHours}<span style="font-size:16px;color:var(--text-tertiary)">h</span></div>
            <div class="stat-sub">${estHours}h estimated</div>
          </div>
        </div>

        <div class="status-breakdown">
          <div class="status-breakdown-title">Status Distribution</div>
          <div class="status-bar-track">
            ${segments.map(s =>
              `<div class="status-bar-segment" style="width:${Math.max(s.pct, 1)}%;background:${App.statusColor(s.status)}" title="${s.status}: ${s.count}"></div>`
            ).join('')}
          </div>
          <div class="status-legend">
            ${segments.map(s => `
              <div class="status-legend-item">
                <span class="status-legend-dot" style="background:${App.statusColor(s.status)}"></span>
                ${s.status}<span class="status-legend-count">${s.count}</span>
              </div>
            `).join('')}
          </div>
        </div>

        ${data.avg_iterations > 0 ? `
        <div class="status-breakdown" style="margin-top:var(--sp-4)">
          <div class="status-breakdown-title">Execution Metrics</div>
          <div class="panel-meta-grid" style="font-size:12px">
            <span class="panel-meta-key">avg iterations</span>
            <span class="panel-meta-value">${data.avg_iterations.toFixed(1)}</span>
            <span class="panel-meta-key">total estimate</span>
            <span class="panel-meta-value">${App.formatMinutes(data.total_estimate_minutes)}</span>
            <span class="panel-meta-key">total actual</span>
            <span class="panel-meta-value">${App.formatDuration(data.total_actual_seconds)}</span>
          </div>
        </div>
        ` : ''}
      </div>
    `;
  }

  function refresh(project) {
    const viewport = document.getElementById('viewport');
    if (viewport) render(viewport, project);
  }

  App.registerView('stats', { render, refresh });
})();
