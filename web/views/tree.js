/* ============================================================
   Tree View — Unified decomposition & decision tree
   Shows how goals decompose into tasks, with decision branches,
   success/failure paths, inline progress, and error context.
   ============================================================ */

(() => {
  let treeData = null;

  function render(viewport, project) {
    viewport.innerHTML = '<div class="loading-state">loading\u2026</div>';

    App.API.get(`/api/dashboard/tree/${project}`).then(data => {
      treeData = data;
      viewport.innerHTML = '<div class="view-enter"><div class="tree-container" id="tree-root"></div></div>';
      drawTree(data);
    }).catch(err => {
      viewport.innerHTML = `<div class="empty-state">Failed to load tree<div class="empty-state-hint">${App.escapeHtml(err.message)}</div></div>`;
    });
  }

  function drawTree(data) {
    const container = document.getElementById('tree-root');
    if (!container) return;

    const nodeMap = new Map();
    data.nodes.forEach(n => nodeMap.set(n.id, n));

    // Build hierarchy from roots
    const roots = data.roots
      .map(id => nodeMap.get(id))
      .filter(Boolean)
      .sort((a, b) => a.created_at < b.created_at ? -1 : 1);

    if (roots.length === 0 && data.nodes.length > 0) {
      data.nodes.forEach(n => {
        if (!n.parent_id) roots.push(n);
      });
    }

    let html = '<div class="tree-forest">';

    roots.forEach(root => {
      html += renderTreeNode(root, nodeMap, 0);
    });

    // Orphans with parents that don't exist in this set
    const rendered = new Set();
    function collectRendered(node) {
      rendered.add(node.id);
      (node.children || []).forEach(cid => {
        const child = nodeMap.get(cid);
        if (child) collectRendered(child);
      });
    }
    roots.forEach(r => collectRendered(r));

    const orphans = data.nodes.filter(n => !rendered.has(n.id));
    if (orphans.length > 0) {
      html += '<div class="tree-orphan-label">Other Tasks</div>';
      orphans.forEach(n => {
        html += renderTreeNode(n, nodeMap, 0);
      });
    }

    html += '</div>';
    container.innerHTML = html;

    // Bind interactions
    container.querySelectorAll('.tree-node').forEach(el => {
      el.addEventListener('click', (e) => {
        e.stopPropagation();
        App.openPanel(el.dataset.taskId);
      });
    });

    container.querySelectorAll('.tree-toggle').forEach(btn => {
      btn.addEventListener('click', (e) => {
        e.stopPropagation();
        const children = btn.closest('.tree-branch').querySelector('.tree-children');
        if (children) {
          children.classList.toggle('collapsed');
          btn.textContent = children.classList.contains('collapsed') ? '\u25b6' : '\u25bc';
        }
      });
    });
  }

  // Compute progress stats for a node from its children
  function computeProgress(node, nodeMap) {
    const children = node.children || [];
    if (children.length === 0) return null;

    let completed = 0;
    let failed = 0;
    children.forEach(cid => {
      const child = nodeMap.get(cid);
      if (!child) return;
      if (child.status === 'completed' || child.status === 'done') completed++;
      else if (['failed', 'dod_failed', 'rejected'].includes(child.status)) failed++;
    });

    const total = children.length;
    const pct = total > 0 ? Math.round((completed / total) * 100) : 0;
    let health = 'healthy';
    if (total > 0) {
      if (failed / total > 0.3) health = 'failing';
      else if (failed > 0) health = 'degraded';
    }

    return { completed, total, pct, health };
  }

  function renderTreeNode(node, nodeMap, depth) {
    const hasChildren = node.children && node.children.length > 0;
    const hasDecisions = node.decisions && node.decisions.length > 0;
    const statusColor = App.statusColor(node.status);
    const isFailed = ['failed', 'dod_failed', 'rejected'].includes(node.status);
    const isSuccess = ['completed', 'done'].includes(node.status);
    const isRoot = depth === 0;
    const progress = computeProgress(node, nodeMap);

    let html = `<div class="tree-branch" style="--depth:${depth}">`;

    // Node itself
    html += `
      <div class="tree-node ${isFailed ? 'tree-node-failed' : ''} ${isSuccess ? 'tree-node-success' : ''} ${isRoot ? 'tree-node-root' : ''}" data-task-id="${App.escapeHtml(node.id)}">
        ${hasChildren ? `<button class="tree-toggle">\u25bc</button>` : '<span class="tree-toggle-spacer"></span>'}
        <span class="tree-node-status" style="background:${statusColor}"></span>
        <span class="tree-node-id">${App.escapeHtml(node.id)}</span>
        <span class="tree-node-title">${App.escapeHtml(App.truncate(node.title, 60))}</span>
        ${progress ? renderProgressBadge(progress) : ''}
        <span class="tree-node-badge">${node.status}</span>
        ${isSuccess && node.actual_duration_sec ? `<span class="tree-node-badge">${App.formatDuration(node.actual_duration_sec)}</span>` : ''}
        ${node.has_traces ? '<span class="tree-node-trace-indicator" title="Has execution traces">\u2b24</span>' : ''}
        ${isFailed ? '<span class="tree-node-action" title="Try another path from here">\u21bb</span>' : ''}
      </div>
    `;

    // Error context below failed nodes
    if (isFailed && node.error_log) {
      const firstLine = node.error_log.split('\n')[0];
      html += `<div class="tree-node-error" style="--depth:${depth}">${App.escapeHtml(App.truncate(firstLine, 80))}</div>`;
    }

    // Decision branches (if any)
    if (hasDecisions) {
      node.decisions.forEach(dec => {
        html += `<div class="tree-decision">`;
        html += `<div class="tree-decision-label">${App.escapeHtml(dec.title || 'Decision')}</div>`;
        dec.alternatives.forEach(alt => {
          const selected = alt.selected;
          html += `
            <div class="tree-alt ${selected ? 'tree-alt-selected' : 'tree-alt-rejected'}">
              <span class="tree-alt-icon">${selected ? '\u25c9' : '\u25cb'}</span>
              <span class="tree-alt-label">${App.escapeHtml(alt.label)}</span>
              ${alt.uct_score > 0 ? `<span class="tree-alt-score">uct:${alt.uct_score.toFixed(2)}</span>` : ''}
              ${!selected ? '<span class="tree-alt-action" title="Explore this path">\u2192</span>' : ''}
            </div>
          `;
        });
        html += `</div>`;
      });
    }

    // Children
    if (hasChildren) {
      html += '<div class="tree-children">';
      node.children.forEach(childId => {
        const child = nodeMap.get(childId);
        if (child) {
          html += renderTreeNode(child, nodeMap, depth + 1);
        }
      });
      html += '</div>';
    }

    html += '</div>';
    return html;
  }

  function renderProgressBadge(progress) {
    const fillColor = progress.health === 'failing' ? 'var(--status-failed)' :
                      progress.health === 'degraded' ? 'var(--status-dod-failed)' :
                      'var(--status-completed)';
    return `
      <span class="tree-progress">
        <span class="tree-progress-bar">
          <span class="tree-progress-fill" style="width:${progress.pct}%;background:${fillColor}"></span>
        </span>
        <span class="tree-progress-text">${progress.completed}/${progress.total}</span>
      </span>
    `;
  }

  function refresh(project) {
    const viewport = document.getElementById('viewport');
    if (viewport) render(viewport, project);
  }

  App.registerView('tree', { render, refresh });
})();
