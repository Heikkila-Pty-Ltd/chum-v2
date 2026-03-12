/* ============================================================
   Structure View — DAG (dagre/force) + outline tree + filters
   ============================================================ */

(() => {
  let simulation = null;
  let svg = null;
  let zoomBehavior = null;
  let cachedData = null;
  let cachedTreeData = null;
  let layoutMode = 'dagre'; // 'dagre' | 'force' | 'outline'
  let hideCompleted = true;
  let goalFilter = '';
  let showCriticalPath = false;

  function render(viewport, project) {
    // Reset project-specific state on (re-)render
    goalFilter = '';
    cachedData = null;
    cachedTreeData = null;

    viewport.innerHTML = `
      <div class="structure-container view-enter" id="structure-root">
        <div class="structure-controls">
          <button class="structure-control-btn ${layoutMode === 'dagre' ? 'active' : ''}" data-layout="dagre" title="Hierarchical (H)">H</button>
          <button class="structure-control-btn ${layoutMode === 'force' ? 'active' : ''}" data-layout="force" title="Force (F)">F</button>
          <button class="structure-control-btn ${layoutMode === 'outline' ? 'active' : ''}" data-layout="outline" title="Outline (O)">O</button>
          <span class="structure-separator"></span>
          <button class="structure-control-btn structure-zoom-btn" data-zoom="in" title="Zoom in">+</button>
          <button class="structure-control-btn structure-zoom-btn" data-zoom="reset" title="Reset zoom">\u25a3</button>
          <button class="structure-control-btn structure-zoom-btn" data-zoom="out" title="Zoom out">\u2212</button>
        </div>
        <div class="structure-filters">
          <label class="structure-filter-toggle">
            <input type="checkbox" data-filter="hide-completed" ${hideCompleted ? 'checked' : ''} />
            <span>hide completed</span>
          </label>
          <label class="structure-filter-toggle">
            <input type="checkbox" data-filter="critical-path" ${showCriticalPath ? 'checked' : ''} />
            <span>critical path</span>
          </label>
          <select class="structure-goal-filter" data-filter="goal">
            <option value="">all goals</option>
          </select>
        </div>
      </div>
    `;

    // Tooltip
    let tooltip = document.querySelector('.structure-tooltip');
    if (!tooltip) {
      tooltip = document.createElement('div');
      tooltip.className = 'structure-tooltip';
      document.body.appendChild(tooltip);
    }

    bindControls(viewport, project, tooltip);
    loadAndDraw(project, tooltip);
  }

  function bindControls(viewport, project, tooltip) {
    const container = document.getElementById('structure-root');
    if (!container) return;

    // Layout toggle
    container.querySelectorAll('[data-layout]').forEach(btn => {
      btn.addEventListener('click', () => {
        layoutMode = btn.dataset.layout;
        container.querySelectorAll('[data-layout]').forEach(b => b.classList.toggle('active', b.dataset.layout === layoutMode));
        // Show/hide zoom buttons based on mode
        container.querySelectorAll('.structure-zoom-btn').forEach(b => {
          b.style.display = layoutMode === 'outline' ? 'none' : '';
        });
        loadAndDraw(project, tooltip);
      });
    });

    // Zoom
    container.querySelectorAll('[data-zoom]').forEach(btn => {
      btn.addEventListener('click', () => {
        if (!svg || !zoomBehavior) return;
        if (btn.dataset.zoom === 'in') svg.transition().duration(300).call(zoomBehavior.scaleBy, 1.4);
        else if (btn.dataset.zoom === 'out') svg.transition().duration(300).call(zoomBehavior.scaleBy, 0.7);
        else svg.transition().duration(300).call(zoomBehavior.transform, d3.zoomIdentity);
      });
    });

    // Hide zoom if starting in outline
    if (layoutMode === 'outline') {
      container.querySelectorAll('.structure-zoom-btn').forEach(b => { b.style.display = 'none'; });
    }

    // Filters
    container.querySelector('[data-filter="hide-completed"]').addEventListener('change', (e) => {
      hideCompleted = e.target.checked;
      loadAndDraw(project, tooltip);
    });
    container.querySelector('[data-filter="critical-path"]').addEventListener('change', (e) => {
      showCriticalPath = e.target.checked;
      loadAndDraw(project, tooltip);
    });
    container.querySelector('[data-filter="goal"]').addEventListener('change', (e) => {
      goalFilter = e.target.value;
      loadAndDraw(project, tooltip);
    });
  }

  function loadAndDraw(project, tooltip) {
    if (layoutMode === 'outline') {
      App.API.tree(project).then(data => {
        cachedTreeData = data;
        drawOutline(data);
      }).catch(err => {
        const container = document.getElementById('structure-root');
        if (container) container.innerHTML = App.errorState('structure', err);
      });
    } else {
      App.API.graph(project).then(data => {
        cachedData = data;
        populateGoalFilter(data);
        drawGraph(data, tooltip);
      }).catch(err => {
        const container = document.getElementById('structure-root');
        if (container) container.innerHTML = App.errorState('structure', err);
      });
    }
  }

  function populateGoalFilter(data) {
    const select = document.querySelector('[data-filter="goal"]');
    if (!select) return;

    // Rebuild options each time so live refreshes pick up new goals.
    const prev = select.value;
    while (select.options.length > 1) select.remove(1);

    // Goals are top-level tasks (no parent_id) — use the task hierarchy,
    // not dependency edges, so the dropdown shows actual goals.
    const goals = data.nodes.filter(n => !n.parent_id);
    goals.forEach(g => {
      const opt = document.createElement('option');
      opt.value = g.id;
      opt.textContent = App.truncate(g.title, 30);
      select.appendChild(opt);
    });

    // Restore previous selection if it still exists.
    if (prev && [...select.options].some(o => o.value === prev)) {
      select.value = prev;
    }
  }

  // --- Filter helpers ---

  function filterNodes(nodes, edges) {
    let filtered = [...nodes];
    const nodeMap = new Map(nodes.map(n => [n.id, n]));

    // Hide completed
    if (hideCompleted) {
      filtered = filtered.filter(n => n.status !== 'completed' && n.status !== 'done');
    }

    // Goal filter — keep only the selected goal and its children (via parent_id hierarchy).
    if (goalFilter) {
      const descendants = new Set();
      descendants.add(goalFilter);
      let changed = true;
      while (changed) {
        changed = false;
        filtered.forEach(n => {
          if (n.parent_id && descendants.has(n.parent_id) && !descendants.has(n.id)) {
            descendants.add(n.id);
            changed = true;
          }
        });
      }
      filtered = filtered.filter(n => descendants.has(n.id));
    }

    const validIds = new Set(filtered.map(n => n.id));
    const filteredEdges = edges.filter(e => validIds.has(e.from) && validIds.has(e.to));

    return { nodes: filtered, edges: filteredEdges };
  }

  function computeCriticalPath(nodes, edges) {
    const unfinished = new Set(nodes.filter(n => n.status !== 'completed' && n.status !== 'done').map(n => n.id));
    if (unfinished.size === 0) return new Set();

    // Build adjacency for unfinished nodes.
    // Edge convention: from=dependent, to=prerequisite.
    // "children" = dependents (tasks that depend on this node).
    // "parents" = prerequisites (tasks this node depends on).
    const children = new Map();
    const parents = new Map();
    unfinished.forEach(id => { children.set(id, []); parents.set(id, []); });
    edges.forEach(e => {
      if (unfinished.has(e.from) && unfinished.has(e.to)) {
        children.get(e.to).push(e.from);
        parents.get(e.from).push(e.to);
      }
    });

    // Find longest path via dynamic programming
    const dist = new Map();
    const prev = new Map();
    const sorted = [];
    const visited = new Set();
    const temp = new Set();

    function topoVisit(id) {
      if (visited.has(id)) return;
      if (temp.has(id)) return; // cycle
      temp.add(id);
      (children.get(id) || []).forEach(c => topoVisit(c));
      temp.delete(id);
      visited.add(id);
      sorted.unshift(id);
    }
    unfinished.forEach(id => topoVisit(id));

    sorted.forEach(id => dist.set(id, 0));
    sorted.forEach(id => {
      const d = dist.get(id);
      (children.get(id) || []).forEach(c => {
        if (d + 1 > dist.get(c)) {
          dist.set(c, d + 1);
          prev.set(c, id);
        }
      });
    });

    // Find the node with max distance — that's the end of the critical path.
    // Use -1 so zero-distance nodes (isolated, no edges) are still eligible.
    let maxDist = -1, endNode = null;
    dist.forEach((d, id) => { if (d > maxDist) { maxDist = d; endNode = id; } });

    if (!endNode) return new Set();

    // Trace back
    const path = new Set();
    let cur = endNode;
    while (cur) {
      path.add(cur);
      cur = prev.get(cur);
    }
    return path;
  }

  // --- Graph drawing (dagre + force) ---

  function drawGraph(data, tooltip) {
    const container = document.getElementById('structure-root');
    if (!container) return;

    const { nodes: filteredNodes, edges: filteredEdges } = filterNodes(data.nodes, data.edges);
    const criticalPath = showCriticalPath ? computeCriticalPath(filteredNodes, filteredEdges) : new Set();

    const width = container.clientWidth;
    const height = container.clientHeight;

    if (simulation) { simulation.stop(); simulation = null; }
    container.querySelectorAll('svg').forEach(s => s.remove());
    // Remove any outline content
    const outlineEl = container.querySelector('.structure-outline');
    if (outlineEl) outlineEl.remove();

    if (filteredNodes.length === 0) {
      const empty = document.createElement('div');
      empty.className = 'empty-state';
      empty.textContent = 'No tasks match filters';
      container.appendChild(empty);
      return;
    }

    svg = d3.select(container)
      .insert('svg', '.structure-controls')
      .attr('width', width)
      .attr('height', height);

    const defs = svg.append('defs');
    defs.append('marker')
      .attr('id', 'arrowhead')
      .attr('viewBox', '0 -4 8 8')
      .attr('refX', 20)
      .attr('refY', 0)
      .attr('markerWidth', 6)
      .attr('markerHeight', 6)
      .attr('orient', 'auto')
      .append('path')
      .attr('d', 'M0,-3L7,0L0,3')
      .attr('fill', '#3a3e48');

    const g = svg.append('g');

    zoomBehavior = d3.zoom()
      .scaleExtent([0.15, 4])
      .on('zoom', (event) => g.attr('transform', event.transform));
    svg.call(zoomBehavior);

    const nodeWidth = 140;
    const nodeHeight = 36;

    const nodeMap = new Map();
    const nodes = filteredNodes.map(n => {
      const node = { ...n };
      nodeMap.set(n.id, node);
      return node;
    });

    const links = filteredEdges
      .filter(e => nodeMap.has(e.from) && nodeMap.has(e.to))
      .map(e => ({ source: e.from, target: e.to }));

    if (layoutMode === 'dagre') {
      drawDagre(g, nodes, links, nodeMap, nodeWidth, nodeHeight, width, height, tooltip, criticalPath);
    } else {
      drawForce(g, nodes, links, nodeMap, nodeWidth, nodeHeight, width, height, tooltip, criticalPath);
    }

    svg.on('click', () => App.closePanel());
  }

  function drawDagre(g, nodes, links, nodeMap, nodeWidth, nodeHeight, width, height, tooltip, criticalPath) {
    const dg = new dagre.graphlib.Graph();
    dg.setGraph({ rankdir: 'TB', nodesep: 30, ranksep: 80 });
    dg.setDefaultEdgeLabel(() => ({}));

    nodes.forEach(n => dg.setNode(n.id, { width: nodeWidth, height: nodeHeight }));
    links.forEach(l => dg.setEdge(l.source, l.target));
    dagre.layout(dg);

    nodes.forEach(n => {
      const pos = dg.node(n.id);
      n.x = pos.x;
      n.y = pos.y;
    });

    const line = d3.line().curve(d3.curveBasis);

    g.append('g')
      .selectAll('path')
      .data(links)
      .join('path')
      .attr('class', d => `structure-edge ${showCriticalPath && criticalPath.has(d.source) && criticalPath.has(d.target) ? 'critical-path' : showCriticalPath ? 'faded' : ''}`)
      .attr('d', l => {
        const edge = dg.edge(l.source, l.target);
        const points = edge.points || [];
        if (points.length === 0) {
          const src = nodeMap.get(l.source);
          const tgt = nodeMap.get(l.target);
          return line([[src.x, src.y], [tgt.x, tgt.y]]);
        }
        return line(points.map(p => [p.x, p.y]));
      })
      .attr('marker-end', 'url(#arrowhead)');

    const node = g.append('g')
      .selectAll('g')
      .data(nodes)
      .join('g')
      .attr('class', d => `structure-node ${showCriticalPath && !criticalPath.has(d.id) ? 'faded' : ''} ${showCriticalPath && criticalPath.has(d.id) ? 'critical-path' : ''}`)
      .attr('transform', d => `translate(${d.x},${d.y})`);

    appendNodeElements(node, nodeWidth, nodeHeight, tooltip);
    fitBounds(nodes, width, height);
  }

  function drawForce(g, nodes, links, nodeMap, nodeWidth, nodeHeight, width, height, tooltip, criticalPath) {
    nodes.forEach(n => {
      n.x = width / 2 + (Math.random() - 0.5) * 400;
      n.y = height / 2 + (Math.random() - 0.5) * 400;
    });

    const link = g.append('g')
      .selectAll('line')
      .data(links)
      .join('line')
      .attr('class', d => `structure-edge ${showCriticalPath && criticalPath.has(d.source.id || d.source) && criticalPath.has(d.target.id || d.target) ? 'critical-path' : showCriticalPath ? 'faded' : ''}`);

    const node = g.append('g')
      .selectAll('g')
      .data(nodes)
      .join('g')
      .attr('class', d => `structure-node ${showCriticalPath && !criticalPath.has(d.id) ? 'faded' : ''}`)
      .call(d3.drag()
        .on('start', dragStarted)
        .on('drag', dragged)
        .on('end', dragEnded));

    appendNodeElements(node, nodeWidth, nodeHeight, tooltip);

    simulation = d3.forceSimulation(nodes)
      .force('link', d3.forceLink(links).id(d => d.id).distance(120).strength(0.4))
      .force('charge', d3.forceManyBody().strength(-300))
      .force('center', d3.forceCenter(width / 2, height / 2))
      .force('collision', d3.forceCollide().radius(nodeWidth / 2 + 8))
      .force('y', d3.forceY(height / 2).strength(0.03))
      .on('tick', () => {
        link
          .attr('x1', d => d.source.x)
          .attr('y1', d => d.source.y)
          .attr('x2', d => d.target.x)
          .attr('y2', d => d.target.y);
        node.attr('transform', d => `translate(${d.x},${d.y})`);
      });

    simulation.on('end', () => fitBounds(nodes, width, height));
    setTimeout(() => fitBounds(nodes, width, height), 1500);
  }

  function appendNodeElements(node, nodeWidth, nodeHeight, tooltip) {
    node.append('rect')
      .attr('width', nodeWidth)
      .attr('height', nodeHeight)
      .attr('x', -nodeWidth / 2)
      .attr('y', -nodeHeight / 2)
      .attr('fill', d => {
        const c = App.statusColor(d.status);
        return d3.color(c).darker(2.5).toString();
      })
      .attr('stroke', d => App.statusColor(d.status))
      .attr('stroke-opacity', 0.6);

    node.append('rect')
      .attr('width', 3)
      .attr('height', nodeHeight)
      .attr('x', -nodeWidth / 2)
      .attr('y', -nodeHeight / 2)
      .attr('fill', d => App.statusColor(d.status));

    node.append('text')
      .attr('x', -nodeWidth / 2 + 10)
      .attr('y', -2)
      .text(d => App.truncate(d.title, 18));

    node.append('text')
      .attr('class', 'node-id')
      .attr('x', -nodeWidth / 2 + 10)
      .attr('y', 11)
      .text(d => d.id);

    node.on('click', (event, d) => {
      event.stopPropagation();
      App.openPanel(d.id);
    });

    node.on('mouseenter', (event, d) => {
      tooltip.innerHTML = `
        <div class="structure-tooltip-id">${App.escapeHtml(d.id)}</div>
        <div>${App.escapeHtml(d.title)}</div>
        <div style="margin-top:4px">${App.statusBadge(d.status)}</div>
      `;
      tooltip.classList.add('visible');
    });

    node.on('mousemove', (event) => {
      tooltip.style.left = (event.clientX + 12) + 'px';
      tooltip.style.top = (event.clientY - 10) + 'px';
    });

    node.on('mouseleave', () => {
      tooltip.classList.remove('visible');
    });
  }

  function fitBounds(nodes, width, height) {
    if (!nodes.length || !svg || !zoomBehavior) return;
    const xs = nodes.map(n => n.x);
    const ys = nodes.map(n => n.y);
    const x0 = Math.min(...xs) - 100;
    const x1 = Math.max(...xs) + 100;
    const y0 = Math.min(...ys) - 60;
    const y1 = Math.max(...ys) + 60;

    const bw = x1 - x0;
    const bh = y1 - y0;
    const scale = Math.min(width / bw, height / bh, 1.5) * 0.9;
    const tx = width / 2 - (x0 + bw / 2) * scale;
    const ty = height / 2 - (y0 + bh / 2) * scale;

    svg.transition().duration(500).call(
      zoomBehavior.transform,
      d3.zoomIdentity.translate(tx, ty).scale(scale)
    );
  }

  function dragStarted(event, d) {
    if (!event.active) simulation.alphaTarget(0.1).restart();
    d.fx = d.x;
    d.fy = d.y;
  }

  function dragged(event, d) {
    d.fx = event.x;
    d.fy = event.y;
  }

  function dragEnded(event, d) {
    if (!event.active) simulation.alphaTarget(0);
    d.fx = null;
    d.fy = null;
  }

  // --- Outline mode (from tree.js) ---

  function drawOutline(data) {
    const container = document.getElementById('structure-root');
    if (!container) return;

    // Remove SVG if present
    container.querySelectorAll('svg').forEach(s => s.remove());
    let outlineEl = container.querySelector('.structure-outline');
    if (!outlineEl) {
      outlineEl = document.createElement('div');
      outlineEl.className = 'structure-outline';
      container.appendChild(outlineEl);
    }

    const nodeMap = new Map();
    data.nodes.forEach(n => nodeMap.set(n.id, n));

    const roots = (data.roots || [])
      .map(id => nodeMap.get(id))
      .filter(Boolean)
      .sort((a, b) => a.created_at < b.created_at ? -1 : 1);

    if (roots.length === 0) {
      data.nodes.forEach(n => { if (!n.parent_id) roots.push(n); });
    }

    let html = '<div class="tree-forest">';
    roots.forEach(root => { html += renderTreeNode(root, nodeMap, 0); });

    // Orphans
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
      orphans.forEach(n => { html += renderTreeNode(n, nodeMap, 0); });
    }

    html += '</div>';
    outlineEl.innerHTML = html;

    // Bind interactions
    outlineEl.querySelectorAll('.tree-node').forEach(el => {
      el.addEventListener('click', (e) => {
        e.stopPropagation();
        App.openPanel(el.dataset.taskId);
      });
    });

    outlineEl.querySelectorAll('.tree-toggle').forEach(btn => {
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

  function computeProgress(node, nodeMap) {
    const children = node.children || [];
    if (children.length === 0) return null;

    let completed = 0, failed = 0;
    children.forEach(cid => {
      const child = nodeMap.get(cid);
      if (!child) return;
      if (child.status === 'completed' || child.status === 'done') completed++;
      else if (App.FAILED_STATUSES.includes(child.status)) failed++;
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
    const sColor = App.statusColor(node.status);
    const isFailed = App.FAILED_STATUSES.includes(node.status);
    const isSuccess = node.status === 'completed' || node.status === 'done';
    const isRoot = depth === 0;
    const progress = computeProgress(node, nodeMap);

    // Apply hide-completed filter
    if (hideCompleted && isSuccess && !isRoot) return '';

    let html = `<div class="tree-branch" style="--depth:${depth}">`;

    html += `
      <div class="tree-node ${isFailed ? 'tree-node-failed' : ''} ${isSuccess ? 'tree-node-success' : ''} ${isRoot ? 'tree-node-root' : ''}" data-task-id="${App.escapeHtml(node.id)}">
        ${hasChildren ? `<button class="tree-toggle">\u25bc</button>` : '<span class="tree-toggle-spacer"></span>'}
        <span class="tree-node-status" style="background:${sColor}"></span>
        <span class="tree-node-id">${App.escapeHtml(node.id)}</span>
        <span class="tree-node-title">${App.escapeHtml(App.truncate(node.title, 60))}</span>
        ${progress ? renderProgressBadge(progress) : ''}
        <span class="tree-node-badge">${node.status}</span>
        ${isSuccess && node.actual_duration_sec ? `<span class="tree-node-badge">${App.formatDuration(node.actual_duration_sec)}</span>` : ''}
        ${node.has_traces ? '<span class="tree-node-trace-indicator" title="Has execution traces">\u2b24</span>' : ''}
      </div>
    `;

    if (isFailed && node.error_log) {
      const firstLine = node.error_log.split('\n')[0];
      html += `<div class="tree-node-error" style="--depth:${depth}">${App.escapeHtml(App.truncate(firstLine, 80))}</div>`;
    }

    if (hasDecisions) {
      node.decisions.forEach(dec => {
        html += `<div class="tree-decision">`;
        html += `<div class="tree-decision-label">${App.escapeHtml(dec.title || 'Decision')}</div>`;
        dec.alternatives.forEach(alt => {
          html += `
            <div class="tree-alt ${alt.selected ? 'tree-alt-selected' : 'tree-alt-rejected'}">
              <span class="tree-alt-icon">${alt.selected ? '\u25c9' : '\u25cb'}</span>
              <span class="tree-alt-label">${App.escapeHtml(alt.label)}</span>
              ${alt.uct_score > 0 ? `<span class="tree-alt-score">uct:${alt.uct_score.toFixed(2)}</span>` : ''}
            </div>
          `;
        });
        html += `</div>`;
      });
    }

    if (hasChildren) {
      html += '<div class="tree-children">';
      node.children.forEach(childId => {
        const child = nodeMap.get(childId);
        if (child) html += renderTreeNode(child, nodeMap, depth + 1);
      });
      html += '</div>';
    }

    html += '</div>';
    return html;
  }

  function renderProgressBadge(progress) {
    const fillColor = App.healthColor(progress.health);
    return `
      <span class="tree-progress">
        <span class="tree-progress-bar">
          <span class="tree-progress-fill" style="width:${progress.pct}%;background:${fillColor}"></span>
        </span>
        <span class="tree-progress-text">${progress.completed}/${progress.total}</span>
      </span>
    `;
  }

  // --- Refresh ---

  function graphFingerprint(data) {
    const nodes = data.nodes.map(n => n.id + ':' + n.status + ':' + n.title).sort().join('|');
    const edges = data.edges.map(e => e.from + '>' + e.to).sort().join('|');
    return nodes + '||' + edges;
  }

  function refresh(project) {
    if (layoutMode === 'outline') {
      App.API.tree(project).then(data => {
        cachedTreeData = data;
        drawOutline(data);
      }).catch(() => {});
    } else {
      App.API.graph(project).then(data => {
        if (!cachedData || graphFingerprint(data) !== graphFingerprint(cachedData)) {
          cachedData = data;
          const tooltip = document.querySelector('.structure-tooltip');
          drawGraph(data, tooltip);
        }
      }).catch(() => {});
    }
  }

  App.registerView('structure', { render, refresh });
})();
