/* ============================================================
   Overview — Blockers-first kanban with jarvis integration
   ============================================================ */

(() => {

  const DISMISS_KEY = 'jv-dismissed-failures';
  const KANBAN_COLS = ['backlog', 'ready', 'running', 'review', 'done'];
  const KANBAN_LABELS = { backlog: 'Backlog', ready: 'Ready', running: 'Running', review: 'Review', done: 'Done' };
  const MAX_DONE_VISIBLE = 10;

  // --- Dismissed failures (from jarvis.js) ---
  function getDismissed() {
    try {
      const raw = localStorage.getItem(DISMISS_KEY);
      if (!raw) return {};
      const d = JSON.parse(raw);
      const cutoff = Date.now() - 48 * 3600 * 1000;
      const clean = {};
      for (const [k, v] of Object.entries(d)) {
        if (v > cutoff) clean[k] = v;
      }
      return clean;
    } catch { return {}; }
  }

  function dismiss(detail) {
    const d = getDismissed();
    d[detail] = Date.now();
    localStorage.setItem(DISMISS_KEY, JSON.stringify(d));
  }

  // --- Data loading ---
  async function loadAll(project) {
    const [grouped, summary, actions, state] = await Promise.all([
      App.API.overviewGrouped(project),
      App.API.jarvisSummary(),
      App.API.jarvisActions(),
      App.API.jarvisState(),
    ]);
    const dismissed = getDismissed();
    const filteredActions = actions.filter(a =>
      !(a.type === 'recurring_failure' && dismissed[a.detail])
    );
    return { grouped, summary, actions: filteredActions, state };
  }

  // --- Render ---
  function render(viewport, project) {
    viewport.innerHTML = '<div class="loading-state">loading\u2026</div>';

    loadAll(project).then(data => {
      viewport.innerHTML = renderPage(data);
      bindInteractions(viewport, project);
    }).catch(err => {
      viewport.innerHTML = App.errorState('overview', err);
    });
  }

  function renderPage(data) {
    return `<div class="view-enter overview">
      ${renderActions(data.actions)}
      ${renderFocusStrip(data.summary, data.state)}
      ${renderHero(data.grouped, data.summary)}
      ${App.renderStatusBar(data.grouped.by_status || {}, data.grouped.total || 0)}
      ${renderKanban(data.grouped)}
    </div>`;
  }

  // --- Action Bar (from jarvis.js) ---
  function renderActions(actions) {
    if (!actions || actions.length === 0) {
      return `<div class="jv-actions-clear">Jarvis is autonomous \u2014 nothing blocking</div>`;
    }

    return `<div class="jv-actions">
      <div class="jv-actions-header">Action Required <span class="jv-actions-count">${actions.length}</span></div>
      ${actions.map((a, idx) => {
        const icon = a.type === 'blocked_goal' ? '\u26d4'
                   : a.type === 'pending_action' ? '\u270b'
                   : '\u26a0';
        const urgClass = a.urgency === 'high' ? 'jv-action-high' : 'jv-action-med';
        const meta = [];
        if (a.type === 'blocked_goal') meta.push(`goal #${a.goal_id}`);
        if (a.set_at) meta.push(`set ${App.timeAgo(a.set_at)}`);
        if (a.fail_count) meta.push(`${a.fail_count}x in 48h`);

        return `<div class="jv-action ${urgClass}" data-action-idx="${idx}">
          <span class="jv-action-icon">${icon}</span>
          <div class="jv-action-body">
            <div class="jv-action-title">${App.escapeHtml(a.title)}</div>
            <div class="jv-action-detail">${App.escapeHtml(a.detail)}</div>
            ${meta.length ? `<div class="jv-action-meta">${meta.join(' \u00b7 ')}</div>` : ''}
            <div class="jv-resolve-row">
              <input class="jv-resolve-input" type="text" placeholder="comment (optional)" data-resolve-input="${idx}">
              <button class="jv-resolve-btn" data-resolve="${idx}" data-type="${a.type}" data-goal-id="${a.goal_id || 0}" data-detail="${App.escapeHtml(a.detail || '')}">done</button>
            </div>
          </div>
        </div>`;
      }).join('')}
    </div>`;
  }

  // --- Focus Strip ---
  function renderFocusStrip(summary, state) {
    const entries = state || [];
    const get = (key) => {
      const e = entries.find(s => s.key === key);
      return e ? e.value : '\u2014';
    };

    return `<div class="ov-focus-strip">
      <div class="ov-focus-item">
        <span class="ov-focus-label">focus</span>
        <span class="ov-focus-value">${App.escapeHtml(App.truncate(summary.current_focus || '\u2014', 80))}</span>
      </div>
      <div class="ov-focus-item">
        <span class="ov-focus-label">cycle</span>
        <span class="ov-focus-value">${App.escapeHtml(get('cycle_counter'))}</span>
      </div>
      <div class="ov-focus-item">
        <span class="ov-focus-label">type</span>
        <span class="ov-focus-value">${App.escapeHtml(get('last_cycle_type'))}</span>
      </div>
      <div class="ov-focus-item">
        <span class="ov-focus-label">streak</span>
        <span class="ov-focus-value">${App.escapeHtml(get('focus_streak'))}</span>
      </div>
    </div>`;
  }

  // --- Hero Stats ---
  function renderHero(grouped, summary) {
    const total = grouped.total || 0;
    const byStatus = grouped.by_status || {};
    const completed = (byStatus.completed || 0) + (byStatus.done || 0);
    const completionPct = total > 0 ? Math.round((completed / total) * 100) : 0;
    const running = byStatus.running || 0;
    const vel = grouped.velocity || {};

    return `<div class="ov-hero">
      <div class="ov-hero-stat">
        <span class="ov-hero-value">${total}</span>
        <span class="ov-hero-label">Total</span>
      </div>
      <div class="ov-hero-stat">
        <span class="ov-hero-value">${completionPct}%</span>
        <span class="ov-hero-label">Complete</span>
      </div>
      <div class="ov-hero-stat">
        <span class="ov-hero-value" style="color:${running > 0 ? 'var(--status-running)' : ''}">${running}</span>
        <span class="ov-hero-label">Running</span>
      </div>
      <div class="ov-hero-stat">
        <span class="ov-hero-value">${summary.active_goals || 0}</span>
        <span class="ov-hero-label">Goals</span>
      </div>
      <div class="ov-hero-stat">
        <span class="ov-hero-value">${vel.completed_24h || 0}</span>
        <span class="ov-hero-label">Last 24h</span>
      </div>
    </div>`;
  }

  // --- Kanban Board ---
  function renderKanban(grouped) {
    // Collect all tasks from goals + orphans
    const allTasks = [];
    const goalColors = {};

    (grouped.goals || []).forEach(g => {
      const goalId = g.task.id;
      const goalTitle = g.task.title;
      const goalStatus = g.display_status || g.task.status;
      goalColors[goalId] = App.statusColor(goalStatus);

      (g.children || []).forEach(c => {
        allTasks.push({ ...c, goalId, goalTitle, goalColor: goalColors[goalId] });
      });
    });

    (grouped.orphans || []).forEach(o => {
      allTasks.push({ ...o, goalId: '', goalTitle: 'Ungrouped', goalColor: '#5c5f69' });
    });

    // Bucket tasks into kanban columns
    const columns = {};
    KANBAN_COLS.forEach(col => { columns[col] = []; });

    allTasks.forEach(t => {
      // Failed/rejected/stale: keep in last known column or put in review
      const isFailed = App.ATTENTION_STATUSES.includes(t.status);
      let col = App.STATUS_KANBAN_MAP[t.status];
      if (col === null || col === undefined) {
        col = isFailed ? 'review' : 'backlog';
      }
      columns[col].push(t);
    });

    // Sort within columns: failed first, then by updated_at desc
    Object.values(columns).forEach(tasks => {
      tasks.sort((a, b) => {
        const aFailed = App.ATTENTION_STATUSES.includes(a.status) ? 0 : 1;
        const bFailed = App.ATTENTION_STATUSES.includes(b.status) ? 0 : 1;
        if (aFailed !== bFailed) return aFailed - bFailed;
        return (b.updated_at || '') > (a.updated_at || '') ? 1 : -1;
      });
    });

    // Done column: show only recent N by default
    const doneAll = columns.done;
    const doneVisible = doneAll.slice(0, MAX_DONE_VISIBLE);
    const doneHidden = doneAll.slice(MAX_DONE_VISIBLE);

    return `<div class="ov-kanban">
      ${KANBAN_COLS.map(col => {
        const tasks = col === 'done' ? doneVisible : columns[col];
        const count = col === 'done' ? doneAll.length : tasks.length;
        return `<div class="ov-kanban-col" data-kanban-col="${col}">
          <div class="ov-kanban-col-header">
            <span class="ov-kanban-col-title">${KANBAN_LABELS[col]}</span>
            <span class="ov-kanban-col-count">${count}</span>
          </div>
          <div class="ov-kanban-col-body">
            ${tasks.map(t => renderKanbanCard(t)).join('')}
            ${col === 'done' && doneHidden.length > 0 ? `
              <button class="ov-kanban-show-more" data-show-done>show ${doneHidden.length} more</button>
              <div class="ov-kanban-hidden" data-done-hidden>
                ${doneHidden.map(t => renderKanbanCard(t)).join('')}
              </div>
            ` : ''}
          </div>
        </div>`;
      }).join('')}
    </div>`;
  }

  function renderKanbanCard(t) {
    const isFailed = App.ATTENTION_STATUSES.includes(t.status);
    const isRunning = t.status === 'running';

    let actionsHtml = '';
    if (isRunning) {
      actionsHtml = `<span class="ov-card-actions">
        <button class="ov-action-btn" data-pause="${App.escapeHtml(t.id)}">pause</button>
        <button class="ov-action-btn ov-action-danger" data-kill="${App.escapeHtml(t.id)}">kill</button>
      </span>`;
    } else if (isFailed) {
      actionsHtml = `<span class="ov-card-actions">
        <button class="ov-action-btn" data-retry="${App.escapeHtml(t.id)}">retry</button>
      </span>`;
    }

    return `<div class="ov-kanban-card ${isFailed ? 'ov-kanban-card-failed' : ''}" data-task-id="${App.escapeHtml(t.id)}">
      <div class="ov-kanban-card-header">
        <span class="ov-kanban-card-goal" style="background:${t.goalColor}" title="${App.escapeHtml(t.goalTitle)}"></span>
        <span class="ov-kanban-card-title">${App.escapeHtml(App.truncate(t.title, 50))}</span>
      </div>
      <div class="ov-kanban-card-meta">
        <span class="ov-kanban-card-status">${App.statusDot(t.status)}${t.status}</span>
        <span class="ov-kanban-card-time">${App.timeAgo(t.updated_at)}</span>
        ${actionsHtml}
      </div>
    </div>`;
  }

  // --- Interactions ---
  function bindInteractions(viewport, project) {
    // Task card click -> detail panel
    viewport.querySelectorAll('.ov-kanban-card').forEach(el => {
      el.addEventListener('click', (e) => {
        if (e.target.closest('.ov-action-btn')) return;
        App.openPanel(el.dataset.taskId);
      });
    });

    // Action buttons
    viewport.querySelectorAll('[data-pause]').forEach(btn => {
      btn.addEventListener('click', (e) => {
        e.stopPropagation();
        actionClick(btn, App.API.pause(btn.dataset.pause), project);
      });
    });

    viewport.querySelectorAll('[data-kill]').forEach(btn => {
      btn.addEventListener('click', (e) => {
        e.stopPropagation();
        if (!confirm('Kill this task?')) return;
        actionClick(btn, App.API.kill(btn.dataset.kill, 'killed via dashboard'), project);
      });
    });

    viewport.querySelectorAll('[data-retry]').forEach(btn => {
      btn.addEventListener('click', (e) => {
        e.stopPropagation();
        actionClick(btn, App.API.retry(btn.dataset.retry), project);
      });
    });

    // Show more done
    viewport.querySelectorAll('[data-show-done]').forEach(btn => {
      btn.addEventListener('click', () => {
        const hidden = viewport.querySelector('[data-done-hidden]');
        if (hidden) {
          hidden.classList.toggle('ov-kanban-visible');
          btn.textContent = hidden.classList.contains('ov-kanban-visible') ? 'hide' : btn.textContent;
        }
      });
    });

    // Resolve buttons (jarvis actions)
    viewport.querySelectorAll('[data-resolve]').forEach(btn => {
      btn.addEventListener('click', (e) => {
        e.stopPropagation();
        const idx = btn.dataset.resolve;
        const type = btn.dataset.type;
        const goalId = parseInt(btn.dataset.goalId, 10) || 0;
        const detail = btn.dataset.detail;
        const input = viewport.querySelector(`[data-resolve-input="${idx}"]`);
        const comment = input ? input.value.trim() : '';

        btn.disabled = true;
        btn.textContent = '\u2026';

        App.API.jarvisResolve({ type, goal_id: goalId, detail, comment }).then(() => {
          if (type === 'recurring_failure') dismiss(detail);
          const card = btn.closest('.jv-action');
          if (card) {
            card.style.transition = 'opacity 300ms, max-height 300ms';
            card.style.opacity = '0';
            card.style.maxHeight = card.offsetHeight + 'px';
            setTimeout(() => {
              card.style.maxHeight = '0';
              card.style.overflow = 'hidden';
              card.style.padding = '0';
              card.style.margin = '0';
            }, 50);
            setTimeout(() => refresh(project), 400);
          } else {
            refresh(project);
          }
        }).catch(err => {
          btn.textContent = 'error';
          btn.title = err.message;
          btn.disabled = false;
        });
      });
    });

    // Prevent keyboard shortcuts in resolve inputs
    viewport.querySelectorAll('.jv-resolve-input').forEach(input => {
      input.addEventListener('keydown', (e) => {
        e.stopPropagation();
        if (e.key === 'Enter') {
          const idx = input.dataset.resolveInput;
          const btn = viewport.querySelector(`[data-resolve="${idx}"]`);
          if (btn) btn.click();
        }
      });
    });
  }

  function actionClick(btn, promise, project) {
    btn.textContent = '\u2026';
    btn.disabled = true;
    promise.then(() => {
      btn.textContent = 'sent';
      setTimeout(() => refresh(project), 1000);
    }).catch(err => {
      btn.textContent = 'err';
      btn.title = err.message;
      btn.disabled = false;
    });
  }

  // --- Snapshot / Restore for graceful refresh ---
  function snapshotUIState(viewport) {
    return {
      scrollTop: viewport.scrollTop,
      doneExpanded: !!viewport.querySelector('[data-done-hidden].ov-kanban-visible'),
    };
  }

  function restoreUIState(viewport, state) {
    if (!state) return;
    if (state.doneExpanded) {
      const hidden = viewport.querySelector('[data-done-hidden]');
      const btn = viewport.querySelector('[data-show-done]');
      if (hidden) hidden.classList.add('ov-kanban-visible');
      if (btn) btn.textContent = 'hide';
    }
    viewport.scrollTop = state.scrollTop;
  }

  // --- Refresh ---
  function refresh(project) {
    const viewport = document.getElementById('viewport');
    if (!viewport) return;
    const uiState = snapshotUIState(viewport);
    loadAll(project).then(data => {
      viewport.innerHTML = renderPage(data);
      bindInteractions(viewport, project);
      restoreUIState(viewport, uiState);
    }).catch(() => {});
  }

  App.registerView('overview', { render, refresh });
})();
