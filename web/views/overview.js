/* ============================================================
   Overview v2 — Grouped goal pipelines with mission control hero
   ============================================================ */

(() => {

  function render(viewport, project) {
    viewport.innerHTML = '<div class="loading-state">loading\u2026</div>';

    App.API.overviewGrouped(project).then(data => {
      viewport.innerHTML = renderOverview(data);
      bindInteractions(viewport, project);
    }).catch(err => {
      viewport.innerHTML = `<div class="empty-state">Failed to load overview<div class="empty-state-hint">${App.escapeHtml(err.message)}</div></div>`;
    });
  }

  /* --- Snapshot / Restore for graceful refresh --- */

  function snapshotUIState(viewport) {
    const state = {
      scrollTop: viewport.scrollTop,
      collapsedGoals: new Set(),
      expandedGoals: new Set(),
      visibleCompletedLists: new Set(),
      visibleOrphanCompleted: false,
    };
    viewport.querySelectorAll('[data-goal-children]').forEach(el => {
      const goalId = el.dataset.goalChildren;
      if (el.classList.contains('collapsed')) {
        state.collapsedGoals.add(goalId);
      } else {
        state.expandedGoals.add(goalId);
      }
    });
    viewport.querySelectorAll('[data-completed-list]').forEach(el => {
      if (el.classList.contains('visible')) {
        state.visibleCompletedLists.add(el.dataset.completedList);
      }
    });
    const orphanList = viewport.querySelector('[data-orphan-completed-list]');
    if (orphanList && orphanList.classList.contains('visible')) {
      state.visibleOrphanCompleted = true;
    }
    return state;
  }

  function restoreUIState(viewport, state) {
    if (!state) return;
    viewport.querySelectorAll('[data-goal-children]').forEach(el => {
      const goalId = el.dataset.goalChildren;
      const toggle = viewport.querySelector(`[data-toggle-goal="${goalId}"] .ov2-goal-toggle`);
      if (state.collapsedGoals.has(goalId)) {
        el.classList.add('collapsed');
        if (toggle) toggle.textContent = '\u25b6';
      } else if (state.expandedGoals.has(goalId)) {
        el.classList.remove('collapsed');
        if (toggle) toggle.textContent = '\u25bc';
      }
    });
    viewport.querySelectorAll('[data-completed-list]').forEach(el => {
      const goalId = el.dataset.completedList;
      if (state.visibleCompletedLists.has(goalId)) {
        el.classList.add('visible');
        const showBtn = viewport.querySelector(`[data-show-completed="${goalId}"]`);
        if (showBtn) showBtn.textContent = 'hide completed';
      }
    });
    if (state.visibleOrphanCompleted) {
      const orphanList = viewport.querySelector('[data-orphan-completed-list]');
      const orphanBtn = viewport.querySelector('[data-show-orphan-completed]');
      if (orphanList) {
        orphanList.classList.add('visible');
        if (orphanBtn) orphanBtn.textContent = 'hide completed/other';
      }
    }
    viewport.scrollTop = state.scrollTop;
  }

  /* --- Render --- */

  function renderOverview(data) {
    const total = data.total || 0;
    const byStatus = data.by_status || {};
    const completed = (byStatus.completed || 0) + (byStatus.done || 0);
    const completionPct = total > 0 ? Math.round((completed / total) * 100) : 0;
    const running = byStatus.running || 0;
    const queued = (byStatus.ready || 0) + (byStatus.open || 0);
    const attentionCount = (byStatus.failed || 0) + (byStatus.dod_failed || 0) +
      (byStatus.needs_refinement || 0) + (byStatus.needs_review || 0) + (byStatus.rejected || 0);
    const vel = data.velocity || {};

    return `<div class="view-enter overview">

      <!-- Hero Bar -->
      <div class="ov2-hero">
        <div class="ov2-hero-stat">
          <span class="ov2-hero-value">${total}</span>
          <span class="ov2-hero-label">Total</span>
        </div>
        <div class="ov2-hero-stat">
          <span class="ov2-hero-value">${completionPct}%</span>
          <span class="ov2-hero-label">Complete</span>
        </div>
        <div class="ov2-hero-stat">
          <span class="ov2-hero-value" style="color:${running > 0 ? 'var(--status-running)' : ''}">${running}</span>
          <span class="ov2-hero-label">Running</span>
        </div>
        <div class="ov2-hero-stat">
          <span class="ov2-hero-value" style="color:${attentionCount > 0 ? 'var(--status-failed)' : ''}">${attentionCount}</span>
          <span class="ov2-hero-label">Attention</span>
        </div>
        <div class="ov2-hero-stat">
          <span class="ov2-hero-value" style="color:${queued > 0 ? 'var(--status-ready)' : ''}">${queued}</span>
          <span class="ov2-hero-label">Queued</span>
        </div>
        <div class="ov2-hero-stat">
          <span class="ov2-hero-value">${vel.completed_24h || 0}</span>
          <span class="ov2-hero-label">Last 24h</span>
        </div>
      </div>

      <!-- Status Bar -->
      ${renderStatusBar(byStatus, total)}

      <!-- Goal Pipelines -->
      ${data.goals.length > 0 ? data.goals.map(g => renderGoal(g)).join('') : ''}

      <!-- Orphan Sections -->
      ${renderOrphans(data.orphans)}

    </div>`;
  }

  function renderStatusBar(byStatus, total) {
    const statusOrder = ['completed', 'done', 'running', 'ready', 'open', 'decomposed', 'needs_refinement', 'needs_review', 'dod_failed', 'failed', 'rejected', 'stale'];
    const segments = statusOrder
      .filter(s => byStatus[s] > 0)
      .map(s => ({ status: s, count: byStatus[s], pct: total > 0 ? (byStatus[s] / total) * 100 : 0 }));

    return `<div class="ov2-status-bar">
      ${segments.map(s =>
        `<div class="status-bar-segment" style="width:${Math.max(s.pct, 1)}%;background:${App.statusColor(s.status)}" title="${s.status}: ${s.count}"></div>`
      ).join('')}
    </div>`;
  }

  function renderGoal(g) {
    const task = g.task;
    const displayStatus = g.display_status || task.status;
    const pct = g.subtask_total > 0 ? Math.round((g.subtask_completed / g.subtask_total) * 100) : 0;
    const allComplete = g.subtask_total > 0 && g.subtask_completed === g.subtask_total;
    const healthClass = `ov2-health-${g.health}`;

    const activeChildren = g.children.filter(c => c.status !== 'completed' && c.status !== 'done');
    const completedChildren = g.children.filter(c => c.status === 'completed' || c.status === 'done');

    const progressColor = g.health === 'failing' ? 'var(--status-failed)' :
                          g.health === 'degraded' ? 'var(--status-dod-failed)' :
                          'var(--status-completed)';

    const estHtml = g.total_estimate_minutes > 0
      ? `<span class="ov2-goal-estimate">${App.formatMinutes(g.total_estimate_minutes)} est</span>`
      : '';
    const actualHtml = g.total_actual_duration_sec > 0
      ? `<span class="ov2-goal-actual">${App.formatDuration(g.total_actual_duration_sec)} actual</span>`
      : '';

    return `
    <div class="ov2-goal" data-goal-id="${App.escapeHtml(task.id)}">
      <div class="ov2-goal-header" data-toggle-goal="${App.escapeHtml(task.id)}">
        <button class="ov2-goal-toggle">${allComplete ? '\u25b6' : '\u25bc'}</button>
        <span class="ov2-goal-status" style="background:${App.statusColor(displayStatus)}"></span>
        <span class="ov2-goal-title">${App.escapeHtml(task.title)}</span>
        <span class="ov2-goal-progress">
          <span class="ov2-goal-progress-bar">
            <span class="ov2-goal-progress-fill" style="width:${pct}%;background:${progressColor}"></span>
          </span>
          <span class="ov2-goal-progress-text">${g.subtask_completed}/${g.subtask_total}</span>
        </span>
        ${estHtml}
        ${actualHtml}
        <span class="ov2-goal-health ${healthClass}">${g.health}</span>
      </div>
      <div class="ov2-goal-children ${allComplete ? 'collapsed' : ''}" data-goal-children="${App.escapeHtml(task.id)}">
        ${activeChildren.map(c => renderChild(c)).join('')}
        ${completedChildren.length > 0 ? `
          <div class="ov2-show-completed" data-show-completed="${App.escapeHtml(task.id)}">
            show ${completedChildren.length} completed
          </div>
          <div class="ov2-completed-list" data-completed-list="${App.escapeHtml(task.id)}">
            ${completedChildren.map(c => renderChild(c)).join('')}
          </div>
        ` : ''}
      </div>
    </div>`;
  }

  function renderChild(c) {
    const isFailed = ['failed', 'dod_failed', 'rejected', 'needs_refinement', 'needs_review'].includes(c.status);
    const isRunning = c.status === 'running';
    const labelsHtml = (c.labels && c.labels.length > 0)
      ? c.labels.map(l => `<span class="ov2-label">${App.escapeHtml(l)}</span>`).join('')
      : '';

    return `
    <div class="ov2-child" data-task-id="${App.escapeHtml(c.id)}">
      <span class="ov2-child-status" style="background:${App.statusColor(c.status)}"></span>
      <span class="ov2-child-title">${App.escapeHtml(c.title)}</span>
      ${labelsHtml}
      ${c.error_log ? `<span class="ov2-child-error" title="${App.escapeHtml(c.error_log)}">${App.escapeHtml(App.truncate(c.error_log, 40))}</span>` : ''}
      <span class="ov2-child-meta">${App.timeAgo(c.updated_at)}</span>
      ${isRunning ? `
        <span class="ov2-child-actions">
          <button class="ov2-action-btn" data-pause="${App.escapeHtml(c.id)}">pause</button>
          <button class="ov2-action-btn ov2-action-danger" data-kill="${App.escapeHtml(c.id)}">kill</button>
          <button class="ov2-action-btn" data-decompose="${App.escapeHtml(c.id)}">decompose</button>
        </span>
      ` : ''}
      ${isFailed ? `
        <span class="ov2-child-actions">
          <button class="ov2-action-btn" data-retry="${App.escapeHtml(c.id)}">retry</button>
          <button class="ov2-action-btn" data-suggest="${App.escapeHtml(c.id)}">suggest</button>
        </span>
      ` : ''}
    </div>`;
  }

  function renderOrphans(orphans) {
    if (!orphans || orphans.length === 0) return '';

    const attention = orphans.filter(o =>
      ['failed', 'dod_failed', 'rejected', 'needs_refinement', 'needs_review'].includes(o.status));
    const running = orphans.filter(o => o.status === 'running');
    const backlog = orphans.filter(o => o.status === 'ready' || o.status === 'open');
    const rest = orphans.filter(o =>
      !['failed','dod_failed','rejected','needs_refinement','needs_review',
        'running','ready','open'].includes(o.status));

    let html = '';

    if (attention.length > 0) {
      html += `
      <div class="ov2-section ov2-section-attention">
        <div class="ov2-section-title">
          Needs Attention <span class="ov2-section-count">${attention.length}</span>
        </div>
        ${attention.map(c => renderChild(c)).join('')}
      </div>`;
    }

    if (running.length > 0) {
      html += `
      <div class="ov2-section ov2-section-running">
        <div class="ov2-section-title">
          Running (ungrouped) <span class="ov2-section-count">${running.length}</span>
        </div>
        ${running.map(c => renderChild(c)).join('')}
      </div>`;
    }

    if (backlog.length > 0) {
      html += `
      <div class="ov2-section ov2-section-backlog">
        <div class="ov2-section-title">
          Backlog <span class="ov2-section-count">${backlog.length}</span>
        </div>
        ${backlog.map(c => renderChild(c)).join('')}
      </div>`;
    }

    if (rest.length > 0) {
      html += `
      <div class="ov2-section">
        <div class="ov2-show-completed" data-show-orphan-completed>
          show ${rest.length} completed/other
        </div>
        <div class="ov2-completed-list" data-orphan-completed-list>
          ${rest.map(c => renderChild(c)).join('')}
        </div>
      </div>`;
    }

    return html;
  }

  function bindInteractions(viewport, project) {
    // Goal header toggle
    viewport.querySelectorAll('.ov2-goal-header').forEach(header => {
      header.addEventListener('click', (e) => {
        if (e.target.closest('.ov2-action-btn')) return;
        const goalId = header.dataset.toggleGoal;
        const children = viewport.querySelector(`[data-goal-children="${goalId}"]`);
        const toggle = header.querySelector('.ov2-goal-toggle');
        if (children) {
          children.classList.toggle('collapsed');
          toggle.textContent = children.classList.contains('collapsed') ? '\u25b6' : '\u25bc';
        }
      });
    });

    // Task click -> open panel
    viewport.querySelectorAll('.ov2-child').forEach(el => {
      el.addEventListener('click', (e) => {
        if (e.target.closest('.ov2-action-btn')) return;
        App.openPanel(el.dataset.taskId);
      });
    });

    // Show completed toggle
    viewport.querySelectorAll('.ov2-show-completed').forEach(el => {
      el.addEventListener('click', () => {
        const goalId = el.dataset.showCompleted;
        const list = viewport.querySelector(`[data-completed-list="${goalId}"]`);
        if (list) {
          const visible = list.classList.toggle('visible');
          el.textContent = visible ? 'hide completed' : `show ${list.children.length} completed`;
        }
      });
    });

    // Orphan completed toggle
    viewport.querySelectorAll('[data-show-orphan-completed]').forEach(el => {
      el.addEventListener('click', () => {
        const list = viewport.querySelector('[data-orphan-completed-list]');
        if (list) {
          const visible = list.classList.toggle('visible');
          el.textContent = visible ? 'hide completed/other' : `show ${list.children.length} completed/other`;
        }
      });
    });

    // Pause button
    viewport.querySelectorAll('[data-pause]').forEach(btn => {
      btn.addEventListener('click', (e) => {
        e.stopPropagation();
        const taskId = btn.dataset.pause;
        btn.textContent = '\u2026';
        btn.disabled = true;
        App.API.post(`/api/dashboard/task/${taskId}/pause`).then(() => {
          btn.textContent = 'paused';
          setTimeout(() => refresh(project), 1000);
        }).catch(err => {
          btn.textContent = 'err';
          btn.title = err.message;
          btn.disabled = false;
        });
      });
    });

    // Kill button
    viewport.querySelectorAll('[data-kill]').forEach(btn => {
      btn.addEventListener('click', (e) => {
        e.stopPropagation();
        const taskId = btn.dataset.kill;
        if (!confirm('Kill this task?')) return;
        btn.textContent = '\u2026';
        btn.disabled = true;
        App.API.post(`/api/dashboard/task/${taskId}/kill`, { reason: 'killed via dashboard' }).then(() => {
          btn.textContent = 'killed';
          setTimeout(() => refresh(project), 1000);
        }).catch(err => {
          btn.textContent = 'err';
          btn.title = err.message;
          btn.disabled = false;
        });
      });
    });

    // Decompose button
    viewport.querySelectorAll('[data-decompose]').forEach(btn => {
      btn.addEventListener('click', (e) => {
        e.stopPropagation();
        const taskId = btn.dataset.decompose;
        btn.textContent = '\u2026';
        btn.disabled = true;
        App.API.post(`/api/dashboard/task/${taskId}/decompose`).then(() => {
          btn.textContent = 'sent';
          setTimeout(() => refresh(project), 1500);
        }).catch(err => {
          btn.textContent = 'err';
          btn.title = err.message;
          btn.disabled = false;
        });
      });
    });

    // Retry button
    viewport.querySelectorAll('[data-retry]').forEach(btn => {
      btn.addEventListener('click', (e) => {
        e.stopPropagation();
        const taskId = btn.dataset.retry;
        btn.textContent = '\u2026';
        btn.disabled = true;
        App.API.retry(taskId).then(() => {
          btn.textContent = 'sent';
          setTimeout(() => refresh(project), 1500);
        }).catch(err => {
          btn.textContent = 'err';
          btn.title = err.message;
          btn.disabled = false;
        });
      });
    });

    // Suggest button
    viewport.querySelectorAll('[data-suggest]').forEach(btn => {
      btn.addEventListener('click', (e) => {
        e.stopPropagation();
        const taskId = btn.dataset.suggest;
        btn.textContent = '\u2026';
        btn.disabled = true;

        App.API.get(`/api/dashboard/suggest/${taskId}`).then(data => {
          btn.textContent = 'suggest';
          btn.disabled = false;
          const childEl = btn.closest('.ov2-child');
          let existing = childEl.nextElementSibling;
          if (existing && existing.classList.contains('ov2-suggest-inline')) {
            existing.remove();
          }
          const div = document.createElement('div');
          div.className = 'ov2-suggest-inline';
          div.textContent = data.suggestion;
          childEl.after(div);
        }).catch(err => {
          btn.textContent = 'err';
          btn.title = err.message;
          btn.disabled = false;
        });
      });
    });
  }

  function refresh(project) {
    const viewport = document.getElementById('viewport');
    if (!viewport) return;
    const uiState = snapshotUIState(viewport);
    App.API.overviewGrouped(project).then(data => {
      viewport.innerHTML = renderOverview(data);
      bindInteractions(viewport, project);
      restoreUIState(viewport, uiState);
    }).catch(() => {});
  }

  App.registerView('overview', { render, refresh });
})();
