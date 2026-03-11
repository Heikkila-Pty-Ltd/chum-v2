/* ============================================================
   Jarvis Knowledge Base — Goals, Facts, Initiatives, Agent State
   ============================================================ */

(() => {

  const DISMISS_KEY = 'jv-dismissed-failures';

  function getDismissed() {
    try {
      const raw = localStorage.getItem(DISMISS_KEY);
      if (!raw) return {};
      const d = JSON.parse(raw);
      // Expire entries older than 48h.
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

  async function loadAll() {
    const [actions, summary, goals, facts, initiatives, state] = await Promise.all([
      App.API.jarvisActions(),
      App.API.jarvisSummary(),
      App.API.jarvisGoals(),
      App.API.jarvisFacts(),
      App.API.jarvisInitiatives(),
      App.API.jarvisState(),
    ]);
    // Filter out dismissed recurring failures.
    const dismissed = getDismissed();
    const filtered = actions.filter(a =>
      !(a.type === 'recurring_failure' && dismissed[a.detail])
    );
    return { actions: filtered, summary, goals, facts, initiatives, state };
  }

  function render(viewport) {
    viewport.innerHTML = '<div class="loading-state">loading\u2026</div>';

    loadAll().then(data => {
      viewport.innerHTML = renderPage(data);
      bindInteractions(viewport);
    }).catch(err => {
      viewport.innerHTML = `<div class="empty-state">Failed to load Jarvis KB<div class="empty-state-hint">${App.escapeHtml(err.message)}</div></div>`;
    });
  }

  function renderPage(data) {
    return `<div class="view-enter jv-page">
      ${renderActions(data.actions)}
      ${renderHero(data.summary)}
      ${renderState(data.state)}
      ${renderGoals(data.goals)}
      ${renderFacts(data.facts)}
      ${renderInitiatives(data.initiatives)}
    </div>`;
  }

  /* --- Action Required (top of page) --- */
  function renderActions(actions) {
    if (!actions || actions.length === 0) {
      return `<div class="jv-actions-clear">Nothing blocking \u2014 Jarvis is autonomous</div>`;
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

  /* --- Hero Summary --- */
  function renderHero(s) {
    return `<div class="ov2-hero">
      <div class="ov2-hero-stat">
        <span class="ov2-hero-value">${s.active_goals}</span>
        <span class="ov2-hero-label">Active Goals</span>
      </div>
      <div class="ov2-hero-stat">
        <span class="ov2-hero-value">${s.total_facts}</span>
        <span class="ov2-hero-label">Facts</span>
      </div>
      <div class="ov2-hero-stat">
        <span class="ov2-hero-value">${s.recent_outcomes}</span>
        <span class="ov2-hero-label">Last 24h</span>
      </div>
      <div class="ov2-hero-stat">
        <span class="ov2-hero-value" style="font-size:14px">${App.escapeHtml(App.truncate(s.current_focus, 60))}</span>
        <span class="ov2-hero-label">Focus</span>
      </div>
    </div>`;
  }

  /* --- Agent State --- */
  function renderState(entries) {
    if (!entries || entries.length === 0) return '';
    const highlight = ['current_focus', 'bootstrap_phase', 'cycle_counter', 'last_cycle_type', 'focus_streak', 'last_cycle_summary'];
    const top = entries.filter(e => highlight.includes(e.key));
    const rest = entries.filter(e => !highlight.includes(e.key));

    return `<div class="jv-section">
      <div class="jv-section-title">Agent State</div>
      <div class="jv-state-grid">
        ${top.map(e => `
          <div class="jv-state-key">${App.escapeHtml(e.key)}</div>
          <div class="jv-state-val">${App.escapeHtml(App.truncate(e.value, 120))}</div>
        `).join('')}
      </div>
      ${rest.length > 0 ? `
        <div class="jv-toggle" data-jv-toggle="state-rest">show ${rest.length} more</div>
        <div class="jv-collapsible" data-jv-panel="state-rest">
          <div class="jv-state-grid">
            ${rest.map(e => `
              <div class="jv-state-key">${App.escapeHtml(e.key)}</div>
              <div class="jv-state-val">${App.escapeHtml(App.truncate(e.value, 200))}</div>
            `).join('')}
          </div>
        </div>
      ` : ''}
    </div>`;
  }

  /* --- Goals --- */
  function renderGoals(goals) {
    if (!goals || goals.length === 0) return '<div class="jv-section"><div class="jv-section-title">Goals</div><div class="empty-state">No active goals</div></div>';

    return `<div class="jv-section">
      <div class="jv-section-title">Goals <span class="jv-section-count">${goals.length}</span></div>
      <div class="jv-goals">
        ${goals.map(g => {
          const pct = Math.round(g.progress);
          const priBadge = g.priority >= 8 ? 'jv-pri-high' : g.priority >= 5 ? 'jv-pri-mid' : 'jv-pri-low';
          const isBlocked = g.blocked_reason && g.blocked_reason.length > 0;
          return `<div class="jv-goal-card ${isBlocked ? 'jv-goal-blocked-card' : ''}">
            <div class="jv-goal-header">
              <span class="jv-pri ${priBadge}">P${g.priority}</span>
              <span class="jv-goal-title">${App.escapeHtml(g.title)}</span>
              <span class="jv-goal-status jv-status-${g.status}">${g.status}</span>
            </div>
            ${g.description ? `<div class="jv-goal-desc">${App.escapeHtml(App.truncate(g.description, 200))}</div>` : ''}
            <div class="jv-goal-progress-row">
              <div class="jv-goal-bar"><div class="jv-goal-bar-fill" style="width:${pct}%"></div></div>
              <span class="jv-goal-pct">${pct}%</span>
            </div>
            ${isBlocked ? `<div class="jv-goal-blocked">\u26d4 blocked: ${App.escapeHtml(g.blocked_reason)}</div>` : ''}
            <div class="jv-goal-meta">${g.category ? g.category + ' \u00b7 ' : ''}updated ${App.timeAgo(g.updated_at)}</div>
          </div>`;
        }).join('')}
      </div>
    </div>`;
  }

  /* --- Facts --- */
  function renderFacts(facts) {
    if (!facts || facts.length === 0) return '';

    const groups = {};
    facts.forEach(f => {
      if (!groups[f.category]) groups[f.category] = [];
      groups[f.category].push(f);
    });

    const catOrder = ['directive', 'preference', 'decision', 'constraint', 'context', 'capability', 'general'];
    const sorted = Object.keys(groups).sort((a, b) => {
      const ai = catOrder.indexOf(a), bi = catOrder.indexOf(b);
      return (ai === -1 ? 99 : ai) - (bi === -1 ? 99 : bi);
    });

    return `<div class="jv-section">
      <div class="jv-section-title">Facts <span class="jv-section-count">${facts.length}</span></div>
      ${sorted.map(cat => `
        <div class="jv-fact-group">
          <div class="jv-fact-cat">${App.escapeHtml(cat)} <span class="jv-section-count">${groups[cat].length}</span></div>
          <div class="jv-fact-list">
            ${groups[cat].map(f => `
              <div class="jv-fact">
                <span class="jv-fact-subject">${App.escapeHtml(f.subject)}</span>
                <span class="jv-fact-text">${App.escapeHtml(f.fact)}</span>
              </div>
            `).join('')}
          </div>
        </div>
      `).join('')}
    </div>`;
  }

  /* --- Initiatives --- */
  function renderInitiatives(initiatives) {
    if (!initiatives || initiatives.length === 0) return '';

    return `<div class="jv-section">
      <div class="jv-section-title">Recent Initiatives <span class="jv-section-count">${initiatives.length}</span></div>
      <div class="jv-initiatives">
        ${initiatives.map(i => {
          const outcomeClass = i.outcome === 'success' ? 'jv-out-success' : i.outcome === 'failure' ? 'jv-out-failure' : i.outcome === 'partial' ? 'jv-out-partial' : 'jv-out-pending';
          const dur = i.duration_s > 0 ? App.formatDuration(Math.round(i.duration_s)) : '';
          return `<div class="jv-initiative">
            <span class="jv-out ${outcomeClass}">${i.outcome}</span>
            <span class="jv-init-summary">${App.escapeHtml(App.truncate(i.summary, 120))}</span>
            <span class="jv-init-meta">${dur}${dur ? ' \u00b7 ' : ''}${App.timeAgo(i.started_at)}</span>
          </div>`;
        }).join('')}
      </div>
    </div>`;
  }

  /* --- Interactions --- */
  function bindInteractions(viewport) {
    // Collapsible toggles.
    viewport.querySelectorAll('[data-jv-toggle]').forEach(el => {
      el.addEventListener('click', () => {
        const panelId = el.dataset.jvToggle;
        const panel = viewport.querySelector(`[data-jv-panel="${panelId}"]`);
        if (panel) {
          const visible = panel.classList.toggle('jv-visible');
          el.textContent = visible ? 'hide' : el.textContent.replace('hide', 'show');
        }
      });
    });

    // Resolve buttons.
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
          // Dismiss recurring failures client-side only after server confirms.
          if (type === 'recurring_failure') {
            dismiss(detail);
          }
          // Animate the card out then refresh.
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
            setTimeout(() => refresh(), 400);
          } else {
            refresh();
          }
        }).catch(err => {
          btn.textContent = 'error';
          btn.title = err.message;
          btn.disabled = false;
        });
      });
    });

    // Prevent keyboard shortcuts when typing in comment inputs.
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

  function refresh() {
    const viewport = document.getElementById('viewport');
    if (!viewport) return;
    loadAll().then(data => {
      viewport.innerHTML = renderPage(data);
      bindInteractions(viewport);
    }).catch(() => {});
  }

  App.registerView('jarvis', { render, refresh });
})();
