/* ============================================================
   Steer — "Reprioritize, pause, redirect effort"
   Write-heavy control surface: triage, queue reorder, project pause.
   Uses Alpine.js for declarative reactivity.
   ============================================================ */
(() => {
  const ATTENTION_STATUSES = new Set([
    'failed', 'dod_failed', 'needs_refinement', 'needs_review',
    'rejected', 'quarantined', 'budget_exceeded',
  ]);

  document.addEventListener('alpine:init', () => {
    Alpine.data('steerPage', () => ({
      // State
      loading: true,
      error: null,
      projects: [],
      filterProject: '',

      // Triage
      triageItems: [],
      triageIdx: 0,
      suggestions: {},       // taskID -> { text, loading, error }
      triageActions: {},      // taskID -> 'accepted'|'dismissed'

      // Queue
      runningTasks: [],
      readyTasks: [],
      queueSaving: false,
      queueError: null,

      // Project controls
      projectStats: {},       // name -> { running, queued, paused, total, pauseLoading }
      actionFeedback: null,   // { message, type: 'success'|'error' }

      // Lifecycle
      async init() {
        await this.load();
        this._keyHandler = (e) => this.handleKey(e);
        document.addEventListener('keydown', this._keyHandler);
      },

      destroy() {
        if (this._keyHandler) {
          document.removeEventListener('keydown', this._keyHandler);
        }
      },

      async load() {
        this.loading = true;
        this.error = null;
        try {
          const [projectsRes, overviewRes, tasksRes] = await Promise.allSettled([
            App.API.projects(),
            App.currentProject ? App.API.overview(App.currentProject) : Promise.resolve(null),
            App.currentProject ? App.API.tasks(App.currentProject) : Promise.resolve({ tasks: [] }),
          ]);

          if (projectsRes.status === 'fulfilled') {
            this.projects = projectsRes.value.projects || [];
          }

          // If no project selected and we have projects, use first
          const project = App.currentProject || (this.projects.length > 0 ? this.projects[0] : '');

          // Load data for the active project
          if (project) {
            await this.loadProject(project);
          }
        } catch (err) {
          this.error = err.message;
        }
        this.loading = false;
      },

      async loadProject(project) {
        const [overviewRes, tasksRes] = await Promise.allSettled([
          App.API.overview(project),
          App.API.tasks(project),
        ]);

        if (overviewRes.status === 'fulfilled') {
          const data = overviewRes.value;
          this.triageItems = (data.attention || []).map(t => ({
            ...t,
            project: project,
          }));
          this.triageIdx = 0;

          // Build project stats from overview data
          this.projectStats[project] = {
            running: (data.running || []).length,
            queued: (data.by_status || {}).ready || 0,
            paused: (data.by_status || {}).paused || 0,
            total: data.total || 0,
            pauseLoading: false,
          };
        }

        if (tasksRes.status === 'fulfilled') {
          const allTasks = tasksRes.value.tasks || [];
          this.runningTasks = allTasks
            .filter(t => t.status === 'running')
            .sort((a, b) => a.priority - b.priority);
          this.readyTasks = allTasks
            .filter(t => t.status === 'ready')
            .sort((a, b) => a.priority - b.priority);
        }

        // Load stats for all projects
        if (this.projects.length > 0) {
          await this.loadAllProjectStats();
        }
      },

      async loadAllProjectStats() {
        const results = await Promise.allSettled(
          this.projects.map(p => App.API.overview(p))
        );
        results.forEach((res, i) => {
          if (res.status === 'fulfilled') {
            const data = res.value;
            const name = this.projects[i];
            this.projectStats[name] = {
              running: (data.running || []).length,
              queued: (data.by_status || {}).ready || 0,
              paused: (data.by_status || {}).paused || 0,
              total: data.total || 0,
              pauseLoading: (this.projectStats[name] || {}).pauseLoading || false,
            };
          }
        });
      },

      async refresh() {
        const project = this.filterProject || App.currentProject || (this.projects[0] || '');
        if (project) {
          await this.loadProject(project);
        }
      },

      // --- Triage ---

      get currentTriageItem() {
        const active = this.activeTriageItems;
        return active.length > 0 && this.triageIdx < active.length ? active[this.triageIdx] : null;
      },

      get activeTriageItems() {
        return this.triageItems.filter(t => !this.triageActions[t.id]);
      },

      get triageProgress() {
        const total = this.triageItems.length;
        const resolved = Object.keys(this.triageActions).length;
        return { total, resolved, remaining: total - resolved };
      },

      async fetchSuggestion(taskId) {
        if (this.suggestions[taskId]) return;
        this.suggestions[taskId] = { text: '', loading: true, error: null };
        try {
          const res = await App.API.suggest(taskId);
          this.suggestions[taskId] = { text: res.suggestion || res.text || '', loading: false, error: null };
        } catch (err) {
          this.suggestions[taskId] = { text: '', loading: false, error: err.message };
        }
      },

      getSuggestion(taskId) {
        return this.suggestions[taskId] || null;
      },

      async triageAccept(taskId, action) {
        try {
          if (action === 'retry') {
            await App.API.retry(taskId);
          } else if (action === 'decompose') {
            await App.API.decompose(taskId);
          } else if (action === 'skip') {
            await App.API.kill(taskId, 'skipped via steer triage');
          }
          this.triageActions[taskId] = 'accepted';
          this.showFeedback('Action applied: ' + action, 'success');
        } catch (err) {
          this.showFeedback('Failed: ' + err.message, 'error');
        }
      },

      triageDismiss(taskId) {
        this.triageActions[taskId] = 'dismissed';
      },

      triageNext() {
        const active = this.activeTriageItems;
        if (this.triageIdx < active.length - 1) {
          this.triageIdx++;
        }
      },

      triagePrev() {
        if (this.triageIdx > 0) {
          this.triageIdx--;
        }
      },

      // --- Queue reorder ---

      async moveTask(idx, direction) {
        const targetIdx = idx + direction;
        if (targetIdx < 0 || targetIdx >= this.readyTasks.length) return;

        // Swap in local state (optimistic)
        const tasks = [...this.readyTasks];
        [tasks[idx], tasks[targetIdx]] = [tasks[targetIdx], tasks[idx]];
        this.readyTasks = tasks;

        // Persist
        this.queueSaving = true;
        this.queueError = null;
        try {
          await App.API.queueReorder(tasks.map(t => t.id));
        } catch (err) {
          // Rollback
          [this.readyTasks[idx], this.readyTasks[targetIdx]] = [this.readyTasks[targetIdx], this.readyTasks[idx]];
          this.queueError = err.message;
        }
        this.queueSaving = false;
      },

      // --- Project controls ---

      async toggleProjectPause(name) {
        const stats = this.projectStats[name];
        if (!stats || stats.pauseLoading) return;

        stats.pauseLoading = true;
        try {
          const isPaused = stats.paused > 0 && stats.running === 0 && stats.queued === 0;
          if (isPaused) {
            const res = await App.API.projectResume(name);
            this.showFeedback(name + ': resumed ' + res.affected + ' tasks', 'success');
          } else {
            const res = await App.API.projectPause(name);
            this.showFeedback(name + ': paused ' + res.affected + ' tasks', 'success');
          }
          // Refresh stats
          await this.refresh();
        } catch (err) {
          this.showFeedback('Failed: ' + err.message, 'error');
        }
        stats.pauseLoading = false;
      },

      isProjectPaused(name) {
        const stats = this.projectStats[name];
        if (!stats) return false;
        return stats.paused > 0 && stats.running === 0 && stats.queued === 0;
      },

      // --- Keyboard shortcuts ---

      handleKey(e) {
        // Only handle when steer page is active and not in an input
        if (!document.querySelector('[x-data="steerPage"]')) return;
        if (e.target.tagName === 'INPUT' || e.target.tagName === 'TEXTAREA' || e.target.tagName === 'SELECT') return;

        switch (e.key) {
          case 'j': this.triageNext(); break;
          case 'k': this.triagePrev(); break;
          case 'r': {
            const item = this.currentTriageItem;
            if (item) this.triageAccept(item.id, 'retry');
            break;
          }
          case 'd': {
            const item = this.currentTriageItem;
            if (item) this.triageDismiss(item.id);
            break;
          }
          case 't': {
            const item = this.currentTriageItem;
            if (item) this.fetchSuggestion(item.id);
            break;
          }
        }
      },

      // --- Helpers ---

      showFeedback(message, type) {
        this.actionFeedback = { message, type };
        setTimeout(() => { this.actionFeedback = null; }, 4000);
      },

      timeAgo(ts) { return App.timeAgo(ts); },
      statusColor(s) { return App.statusColor(s); },
      truncate(s, n) { return App.truncate(s, n || 80); },
      escapeHtml(s) { return App.escapeHtml(s); },
    }));
  });

  function render(viewport, project) {
    viewport.innerHTML = `
    <div x-data="steerPage" class="steer-page view-enter">
      <!-- Loading -->
      <template x-if="loading">
        <div class="loading-state">loading\u2026</div>
      </template>

      <!-- Error -->
      <template x-if="error && !loading">
        <div class="empty-state">
          Failed to load steer data
          <div class="empty-state-hint" x-text="error"></div>
        </div>
      </template>

      <!-- Content -->
      <template x-if="!loading && !error">
        <div>
          <!-- Action Feedback Toast -->
          <template x-if="actionFeedback">
            <div class="steer-toast" :class="'steer-toast--' + actionFeedback.type" x-text="actionFeedback.message" x-transition></div>
          </template>

          <!-- Triage Panel -->
          <section class="steer-section">
            <h2 class="steer-section-title">
              Triage
              <span class="steer-section-count" x-text="triageProgress.remaining + ' remaining'"></span>
            </h2>

            <template x-if="triageItems.length === 0">
              <div class="steer-empty">No tasks need attention</div>
            </template>

            <template x-if="triageItems.length > 0 && activeTriageItems.length === 0">
              <div class="steer-empty steer-empty--done">All items triaged</div>
            </template>

            <template x-if="activeTriageItems.length > 0">
              <div class="steer-triage">
                <div class="steer-triage-nav">
                  <button class="steer-btn steer-btn--sm" @click="triagePrev()" :disabled="triageIdx === 0">\u25C0</button>
                  <span class="steer-triage-pos" x-text="(triageIdx + 1) + ' / ' + activeTriageItems.length"></span>
                  <button class="steer-btn steer-btn--sm" @click="triageNext()" :disabled="triageIdx >= activeTriageItems.length - 1">\u25B6</button>
                </div>

                <template x-if="currentTriageItem">
                  <div class="steer-triage-card">
                    <div class="steer-triage-header">
                      <a class="task-link steer-task-id" :href="'#/work/' + currentTriageItem.id" x-text="currentTriageItem.id.slice(0, 12)" @click.stop></a>
                      <span class="status-badge" :style="'background:' + statusColor(currentTriageItem.status)" x-text="currentTriageItem.status"></span>
                      <span class="steer-triage-age" x-text="timeAgo(currentTriageItem.updated_at)"></span>
                    </div>
                    <div class="steer-triage-title" x-text="currentTriageItem.title"></div>
                    <template x-if="currentTriageItem.error_log">
                      <pre class="steer-triage-error" x-text="truncate(currentTriageItem.error_log, 300)"></pre>
                    </template>

                    <!-- Suggestion -->
                    <div class="steer-triage-suggest">
                      <template x-if="!getSuggestion(currentTriageItem.id)">
                        <button class="steer-btn steer-btn--outline" @click="fetchSuggestion(currentTriageItem.id)">
                          <span class="steer-key">t</span> Get Suggestion
                        </button>
                      </template>
                      <template x-if="getSuggestion(currentTriageItem.id)?.loading">
                        <div class="steer-suggest-box steer-suggest-box--loading">Thinking\u2026</div>
                      </template>
                      <template x-if="getSuggestion(currentTriageItem.id)?.error">
                        <div class="steer-suggest-box steer-suggest-box--error" x-text="getSuggestion(currentTriageItem.id).error"></div>
                      </template>
                      <template x-if="getSuggestion(currentTriageItem.id)?.text">
                        <div class="steer-suggest-box">
                          <span class="steer-suggest-label">Suggested</span>
                          <span x-text="getSuggestion(currentTriageItem.id).text"></span>
                        </div>
                      </template>
                    </div>

                    <!-- Action buttons -->
                    <div class="steer-triage-actions">
                      <button class="steer-btn steer-btn--primary" @click="triageAccept(currentTriageItem.id, 'retry')">
                        <span class="steer-key">r</span> Retry
                      </button>
                      <button class="steer-btn" @click="triageAccept(currentTriageItem.id, 'decompose')">Decompose</button>
                      <button class="steer-btn" @click="triageAccept(currentTriageItem.id, 'skip')">Skip</button>
                      <button class="steer-btn steer-btn--ghost" @click="triageDismiss(currentTriageItem.id)">
                        <span class="steer-key">d</span> Dismiss
                      </button>
                    </div>
                  </div>
                </template>
              </div>
            </template>
          </section>

          <!-- Execution Queue -->
          <section class="steer-section">
            <h2 class="steer-section-title">
              Execution Queue
              <template x-if="queueSaving">
                <span class="steer-saving">saving\u2026</span>
              </template>
            </h2>

            <template x-if="queueError">
              <div class="steer-queue-error" x-text="queueError"></div>
            </template>

            <!-- Running tasks -->
            <template x-if="runningTasks.length > 0">
              <div class="steer-queue-group">
                <div class="steer-queue-label">Running</div>
                <template x-for="task in runningTasks" :key="task.id">
                  <div class="steer-queue-item steer-queue-item--running">
                    <span class="steer-pulse"></span>
                    <a class="task-link steer-task-id" :href="'#/work/' + task.id" x-text="task.id.slice(0, 12)" @click.stop></a>
                    <span class="steer-queue-title" x-text="truncate(task.title, 60)"></span>
                    <span class="steer-queue-project" x-text="task.project"></span>
                  </div>
                </template>
              </div>
            </template>

            <!-- Ready tasks (reorderable) -->
            <template x-if="readyTasks.length > 0">
              <div class="steer-queue-group">
                <div class="steer-queue-label">Ready (drag to reorder)</div>
                <template x-for="(task, idx) in readyTasks" :key="task.id">
                  <div class="steer-queue-item">
                    <div class="steer-queue-arrows">
                      <button class="steer-arrow" @click="moveTask(idx, -1)" :disabled="idx === 0" title="Move up">\u25B2</button>
                      <button class="steer-arrow" @click="moveTask(idx, 1)" :disabled="idx === readyTasks.length - 1" title="Move down">\u25BC</button>
                    </div>
                    <span class="steer-queue-pos" x-text="idx + 1"></span>
                    <a class="task-link steer-task-id" :href="'#/work/' + task.id" x-text="task.id.slice(0, 12)" @click.stop></a>
                    <span class="steer-queue-title" x-text="truncate(task.title, 60)"></span>
                    <span class="steer-queue-project" x-text="task.project"></span>
                  </div>
                </template>
              </div>
            </template>

            <template x-if="runningTasks.length === 0 && readyTasks.length === 0">
              <div class="steer-empty">No tasks in queue</div>
            </template>
          </section>

          <!-- Project Controls -->
          <section class="steer-section">
            <h2 class="steer-section-title">Project Controls</h2>

            <template x-if="projects.length === 0">
              <div class="steer-empty">No projects</div>
            </template>

            <div class="steer-projects">
              <template x-for="p in projects" :key="p">
                <div class="steer-project-row">
                  <span class="steer-project-name" x-text="p"></span>
                  <span class="steer-project-stat">
                    <span class="steer-stat-num" x-text="(projectStats[p] || {}).running || 0"></span> running
                  </span>
                  <span class="steer-project-stat">
                    <span class="steer-stat-num" x-text="(projectStats[p] || {}).queued || 0"></span> queued
                  </span>
                  <span class="steer-project-stat" x-show="(projectStats[p] || {}).paused > 0">
                    <span class="steer-stat-num steer-stat-num--warn" x-text="(projectStats[p] || {}).paused || 0"></span> paused
                  </span>
                  <button class="steer-btn steer-btn--sm"
                    :class="{ 'steer-btn--warn': !isProjectPaused(p), 'steer-btn--primary': isProjectPaused(p) }"
                    :disabled="(projectStats[p] || {}).pauseLoading"
                    @click="toggleProjectPause(p)"
                    x-text="isProjectPaused(p) ? 'Resume' : 'Pause'">
                  </button>
                  <button class="steer-btn steer-btn--sm steer-btn--ghost" disabled title="Coming soon">Replan</button>
                </div>
              </template>
            </div>
          </section>

          <!-- Keyboard hints -->
          <div class="steer-keyhints">
            <span><kbd>j</kbd>/<kbd>k</kbd> navigate</span>
            <span><kbd>r</kbd> retry</span>
            <span><kbd>d</kbd> dismiss</span>
            <span><kbd>t</kbd> get suggestion</span>
          </div>
        </div>
      </template>
    </div>`;
    Alpine.initTree(viewport);
  }

  function refresh(project) {
    const el = document.querySelector('[x-data="steerPage"]');
    if (el && el._x_dataStack) el._x_dataStack[0].refresh();
  }

  App.registerView('steer', { render, refresh });
})();
