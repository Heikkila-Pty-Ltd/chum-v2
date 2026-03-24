/* ============================================================
   Tasks View — Table + DAG
   ============================================================ */
(() => {
  let currentData = null;
  let activeTab = 'table'; // 'table' | 'dag'
  let sortCol = 'age';
  let sortAsc = false;
  let filterStatus = '';
  let filterText = '';

  async function loadAll(project) {
    const [tasksRes, graphRes] = await Promise.all([
      App.API.tasks(project),
      App.API.graph(project),
    ]);
    return { tasks: tasksRes.tasks || [], graph: graphRes };
  }

  function render(viewport, project) {
    filterStatus = '';
    filterText = '';
    viewport.innerHTML = '<div class="loading-state">loading\u2026</div>';
    loadAll(project).then(data => {
      currentData = data;
      viewport.innerHTML = renderPage(data);
      bindInteractions(viewport, project);
    }).catch(err => {
      viewport.innerHTML = App.errorState('Failed to load tasks: ' + err.message);
    });
  }

  function renderPage(data) {
    const tasks = applyFilters(data.tasks);
    return `<div class="view-enter tasks-view">
      <div class="tasks-toolbar">
        <div class="tasks-tabs">
          <button class="tasks-tab ${activeTab === 'table' ? 'active' : ''}" data-tab="table">Table</button>
          <button class="tasks-tab ${activeTab === 'dag' ? 'active' : ''}" data-tab="dag">DAG</button>
        </div>
        <div class="tasks-filters">
          <select class="tasks-status-filter" title="Filter by status">
            <option value="">All statuses</option>
            ${App.STATUS_NAMES.map(s => `<option value="${s}" ${filterStatus === s ? 'selected' : ''}>${s}</option>`).join('')}
          </select>
          <input type="text" class="tasks-search" placeholder="Search title\u2026" value="${App.escapeHtml(filterText)}" />
        </div>
      </div>
      <div class="tasks-content">
        ${activeTab === 'table' ? renderTable(tasks) : renderDAG(data.graph)}
      </div>
    </div>`;
  }

  function applyFilters(tasks) {
    let result = tasks;
    if (filterStatus) result = result.filter(t => t.status === filterStatus);
    if (filterText) {
      const q = filterText.toLowerCase();
      result = result.filter(t => (t.title || '').toLowerCase().includes(q) || (t.id || '').toLowerCase().includes(q));
    }
    result = sortTasks(result);
    return result;
  }

  function sortTasks(tasks) {
    const copy = [...tasks];
    copy.sort((a, b) => {
      let va, vb;
      switch (sortCol) {
        case 'status': va = a.status; vb = b.status; break;
        case 'title': va = a.title || ''; vb = b.title || ''; break;
        case 'project': va = a.project || ''; vb = b.project || ''; break;
        case 'attempts': va = a.attempt_count || 0; vb = b.attempt_count || 0; break;
        case 'age': default: va = a.created_at || ''; vb = b.created_at || ''; break;
      }
      if (va < vb) return sortAsc ? -1 : 1;
      if (va > vb) return sortAsc ? 1 : -1;
      return 0;
    });
    return copy;
  }

  function renderTable(tasks) {
    if (tasks.length === 0) return '<div class="empty-state">No tasks match filters</div>';

    const arrow = col => sortCol === col ? (sortAsc ? ' \u25B2' : ' \u25BC') : '';
    return `<table class="tasks-table">
      <thead>
        <tr>
          <th class="sortable" data-col="status">Status${arrow('status')}</th>
          <th class="sortable" data-col="title">Title${arrow('title')}</th>
          <th class="sortable" data-col="project">Project${arrow('project')}</th>
          <th class="sortable" data-col="attempts">Attempts${arrow('attempts')}</th>
          <th class="sortable" data-col="age">Age${arrow('age')}</th>
        </tr>
      </thead>
      <tbody>
        ${tasks.map(t => `<tr class="task-row" data-task-id="${App.escapeHtml(t.id)}">
          <td>${App.statusBadge(t.status)}</td>
          <td class="task-title-cell">${App.escapeHtml(t.title || t.id)}</td>
          <td>${App.escapeHtml(t.project || '')}</td>
          <td>${t.attempt_count || 0}</td>
          <td>${App.timeAgo(t.created_at)}</td>
        </tr>`).join('')}
      </tbody>
    </table>`;
  }

  function renderDAG(graph) {
    if (!graph || !graph.nodes || graph.nodes.length === 0) {
      return '<div class="empty-state">No graph data</div>';
    }
    return `<div class="dag-container">
      <div class="dag-controls">
        <label><input type="checkbox" id="dag-hide-completed" /> Hide completed</label>
      </div>
      <svg id="dag-svg" width="100%" height="600"></svg>
    </div>`;
  }

  function drawDAG(graph, hideCompleted) {
    if (typeof dagre === 'undefined' || typeof d3 === 'undefined') return;
    const svg = d3.select('#dag-svg');
    if (svg.empty()) return;
    svg.selectAll('*').remove();

    let nodes = graph.nodes || [];
    let edges = graph.edges || [];
    if (hideCompleted) {
      const hidden = new Set(nodes.filter(n => n.status === 'completed' || n.status === 'done').map(n => n.id));
      nodes = nodes.filter(n => !hidden.has(n.id));
      edges = edges.filter(e => !hidden.has(e.from) && !hidden.has(e.to));
    }
    if (nodes.length === 0) return;

    const g = new dagre.graphlib.Graph();
    g.setGraph({ rankdir: 'TB', nodesep: 40, ranksep: 60 });
    g.setDefaultEdgeLabel(() => ({}));

    nodes.forEach(n => g.setNode(n.id, { label: n.title || n.id, width: 160, height: 36, status: n.status }));
    edges.forEach(e => g.setEdge(e.from, e.to));
    dagre.layout(g);

    const svgEl = document.getElementById('dag-svg');
    const graphObj = g.graph();
    svgEl.setAttribute('viewBox', `0 0 ${graphObj.width + 40} ${graphObj.height + 40}`);

    const container = svg.append('g').attr('transform', 'translate(20,20)');

    // Edges
    container.selectAll('line.dag-edge')
      .data(g.edges().map(e => g.edge(e)))
      .enter().append('line')
      .attr('class', 'dag-edge')
      .attr('x1', d => d.points[0].x).attr('y1', d => d.points[0].y)
      .attr('x2', d => d.points[d.points.length - 1].x).attr('y2', d => d.points[d.points.length - 1].y)
      .attr('stroke', 'var(--border-default)').attr('stroke-width', 1.5);

    // Nodes
    const nodeGroups = container.selectAll('g.dag-node')
      .data(g.nodes().map(id => ({ id, ...g.node(id) })))
      .enter().append('g')
      .attr('class', 'dag-node')
      .attr('transform', d => `translate(${d.x - d.width / 2},${d.y - d.height / 2})`)
      .style('cursor', 'pointer')
      .on('click', (e, d) => App.openPanel(d.id));

    nodeGroups.append('rect')
      .attr('width', d => d.width).attr('height', d => d.height)
      .attr('rx', 4)
      .attr('fill', 'var(--surface-3)')
      .attr('stroke', d => App.statusColor(d.status))
      .attr('stroke-width', 2);

    nodeGroups.append('text')
      .attr('x', d => d.width / 2).attr('y', d => d.height / 2 + 4)
      .attr('text-anchor', 'middle')
      .attr('fill', 'var(--text-primary)')
      .attr('font-size', '11px')
      .attr('font-family', 'var(--font-mono)')
      .text(d => d.label.length > 22 ? d.label.slice(0, 20) + '\u2026' : d.label);
  }

  function bindInteractions(viewport, project) {
    // Tab switching
    viewport.querySelectorAll('.tasks-tab').forEach(btn => {
      btn.addEventListener('click', () => {
        activeTab = btn.dataset.tab;
        viewport.innerHTML = renderPage(currentData);
        bindInteractions(viewport, project);
        if (activeTab === 'dag') drawDAG(currentData.graph, false);
      });
    });

    // Column sorting
    viewport.querySelectorAll('th.sortable').forEach(th => {
      th.addEventListener('click', () => {
        const col = th.dataset.col;
        if (sortCol === col) sortAsc = !sortAsc;
        else { sortCol = col; sortAsc = true; }
        viewport.querySelector('.tasks-content').innerHTML = renderTable(applyFilters(currentData.tasks));
        bindInteractions(viewport, project);
      });
    });

    // Status filter
    const sel = viewport.querySelector('.tasks-status-filter');
    if (sel) sel.addEventListener('change', () => {
      filterStatus = sel.value;
      viewport.querySelector('.tasks-content').innerHTML = activeTab === 'table'
        ? renderTable(applyFilters(currentData.tasks))
        : renderDAG(currentData.graph);
      bindInteractions(viewport, project);
    });

    // Text search (debounced)
    const search = viewport.querySelector('.tasks-search');
    let searchTimer;
    if (search) {
      search.addEventListener('input', () => {
        clearTimeout(searchTimer);
        searchTimer = setTimeout(() => {
          filterText = search.value;
          if (activeTab === 'table') {
            viewport.querySelector('.tasks-content').innerHTML = renderTable(applyFilters(currentData.tasks));
            // Re-bind only table interactions
            viewport.querySelectorAll('.task-row').forEach(row => {
              row.addEventListener('click', () => App.openPanel(row.dataset.taskId));
            });
          }
        }, 200);
      });
    }

    // Row clicks -> detail panel
    viewport.querySelectorAll('.task-row').forEach(row => {
      row.addEventListener('click', () => App.openPanel(row.dataset.taskId));
    });

    // DAG hide-completed toggle
    const hideCheck = viewport.querySelector('#dag-hide-completed');
    if (hideCheck) {
      hideCheck.addEventListener('change', () => drawDAG(currentData.graph, hideCheck.checked));
    }

    // Draw DAG if active
    if (activeTab === 'dag' && currentData) drawDAG(currentData.graph, false);
  }

  function refresh(project) {
    const viewport = document.getElementById('viewport');
    if (!viewport) return;
    // Snapshot filter state (already in closure variables)
    loadAll(project).then(data => {
      currentData = data;
      const content = viewport.querySelector('.tasks-content');
      if (content) {
        content.innerHTML = activeTab === 'table'
          ? renderTable(applyFilters(data.tasks))
          : renderDAG(data.graph);
        // Re-bind row clicks
        viewport.querySelectorAll('.task-row').forEach(row => {
          row.addEventListener('click', () => App.openPanel(row.dataset.taskId));
        });
        if (activeTab === 'dag') drawDAG(data.graph, document.getElementById('dag-hide-completed')?.checked || false);
      }
    }).catch(() => {});
  }

  App.registerView('tasks', { render, refresh });
})();
