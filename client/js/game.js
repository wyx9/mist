"use strict";

// ═══════════════════════════════════════════════════════════════
// 俄罗斯方块引擎（纯前端玩法）
// 10×20 棋盘、7-bag 随机、SRS 旋转 + 踢墙、DAS/ARR 输入、
// 锁定延迟、幽灵块、暂存、Next×3、消行闪白 + 塌落动画。
//
// 后续服务器同步预留接口：
//   snapshot()                       —— 全量棋盘快照（将来上报服务器）
//   renderMiniBoard(el, snapshot)    —— 观战区渲染（将来喂服务器推送的数据）
//   getOpponentSlots()               —— 观战数据来源（将来替换为服务器数据）
//   receiveGarbage(rows) / onLineClear(lines) —— 攻击/垃圾行钩子（占位注释）
// ═══════════════════════════════════════════════════════════════

(function () {
  const COLS = 10;
  const ROWS = 20;

  const DAS_MS = 150;        // 横移自动重复延迟
  const ARR_MS = 45;         // 横移自动重复间隔
  const LOCK_DELAY_MS = 500; // 落地锁定延迟
  const LOCK_RESET_MAX = 15; // 锁定延迟最大重置次数
  const SOFT_MS = 40;        // 软降重力间隔
  const FLASH_MS = 340;      // 消行闪白时长
  const COLLAPSE_MS = 170;   // 行塌落时长

  const TYPES = ['I', 'J', 'L', 'O', 'S', 'T', 'Z'];

  // 形状表：SHAPES[type][rot] = [[x,y]×4]，4×4 盒内坐标（y 向下）
  const SHAPES = {
    I: [
      [[0, 1], [1, 1], [2, 1], [3, 1]],
      [[2, 0], [2, 1], [2, 2], [2, 3]],
      [[0, 2], [1, 2], [2, 2], [3, 2]],
      [[1, 0], [1, 1], [1, 2], [1, 3]],
    ],
    J: [
      [[0, 0], [0, 1], [1, 1], [2, 1]],
      [[1, 0], [2, 0], [1, 1], [1, 2]],
      [[0, 1], [1, 1], [2, 1], [2, 2]],
      [[1, 0], [1, 1], [0, 2], [1, 2]],
    ],
    L: [
      [[2, 0], [0, 1], [1, 1], [2, 1]],
      [[1, 0], [1, 1], [1, 2], [2, 2]],
      [[0, 1], [1, 1], [2, 1], [0, 2]],
      [[0, 0], [1, 0], [1, 1], [1, 2]],
    ],
    O: [
      [[1, 0], [2, 0], [1, 1], [2, 1]],
      [[1, 0], [2, 0], [1, 1], [2, 1]],
      [[1, 0], [2, 0], [1, 1], [2, 1]],
      [[1, 0], [2, 0], [1, 1], [2, 1]],
    ],
    S: [
      [[1, 0], [2, 0], [0, 1], [1, 1]],
      [[1, 0], [1, 1], [2, 1], [2, 2]],
      [[1, 1], [2, 1], [0, 2], [1, 2]],
      [[0, 0], [0, 1], [1, 1], [1, 2]],
    ],
    T: [
      [[1, 0], [0, 1], [1, 1], [2, 1]],
      [[1, 0], [1, 1], [2, 1], [1, 2]],
      [[0, 1], [1, 1], [2, 1], [1, 2]],
      [[1, 0], [0, 1], [1, 1], [1, 2]],
    ],
    Z: [
      [[0, 0], [1, 0], [1, 1], [2, 1]],
      [[2, 0], [1, 1], [2, 1], [1, 2]],
      [[0, 1], [1, 1], [1, 2], [2, 2]],
      [[1, 0], [0, 1], [1, 1], [0, 2]],
    ],
  };

  // SRS 踢墙表（y 向下，+y 为下）
  const KICKS_JLSTZ = {
    '0>1': [[0, 0], [-1, 0], [-1, 1], [0, -2], [-1, -2]],
    '1>0': [[0, 0], [1, 0], [1, -1], [0, 2], [1, 2]],
    '1>2': [[0, 0], [1, 0], [1, -1], [0, 2], [1, 2]],
    '2>1': [[0, 0], [-1, 0], [-1, 1], [0, -2], [-1, -2]],
    '2>3': [[0, 0], [1, 0], [1, 1], [0, -2], [1, -2]],
    '3>2': [[0, 0], [-1, 0], [-1, -1], [0, 2], [-1, 2]],
    '3>0': [[0, 0], [-1, 0], [-1, -1], [0, 2], [-1, 2]],
    '0>3': [[0, 0], [1, 0], [1, 1], [0, -2], [1, -2]],
  };
  const KICKS_I = {
    '0>1': [[0, 0], [-2, 0], [1, 0], [-2, -1], [1, 2]],
    '1>0': [[0, 0], [2, 0], [-1, 0], [2, 1], [-1, -2]],
    '1>2': [[0, 0], [-1, 0], [2, 0], [-1, 2], [2, -1]],
    '2>1': [[0, 0], [1, 0], [-2, 0], [1, -2], [-2, 1]],
    '2>3': [[0, 0], [2, 0], [-1, 0], [2, 1], [-1, -2]],
    '3>2': [[0, 0], [-2, 0], [1, 0], [-2, -1], [1, 2]],
    '3>0': [[0, 0], [1, 0], [-2, 0], [1, -2], [-2, 1]],
    '0>3': [[0, 0], [-1, 0], [2, 0], [-1, 2], [2, -1]],
  };

  const SCORE_TABLE = [0, 100, 300, 500, 800];

  // ═══ 状态 ═══
  const state = {
    board: [],          // board[y][x] = { t, id } | null
    piece: null,        // { type, rot, x, y }
    hold: null,         // type | null
    holdUsed: false,
    bag: [],            // 7-bag 队列
    score: 0,
    lines: 0,
    level: 1,
    phase: 'idle',      // idle | ready | playing | clearing | paused | over
    soft: false,
    active: false,      // 是否在游戏视图内
    gravityTimer: null,
    lockTimer: null,
    lockResets: 0,
    clearingRows: [],
    leftTimer: null, leftInterval: null,
    rightTimer: null, rightInterval: null,
    cellSeq: 0,
    cellEls: new Map(), // id -> el
  };

  // DOM 引用
  const el = {};

  function $(id) { return document.getElementById(id); }

  // ═══ 工具 ═══
  function shuffleBag() {
    const b = TYPES.slice();
    for (let i = b.length - 1; i > 0; i--) {
      const j = (Math.random() * (i + 1)) | 0;
      [b[i], b[j]] = [b[j], b[i]];
    }
    return b;
  }
  function nextType() {
    if (state.bag.length === 0) state.bag = shuffleBag();
    return state.bag.shift();
  }
  function preview(n) {
    const types = state.bag.slice();
    while (types.length < n) types.push(...shuffleBag());
    return types.slice(0, n);
  }

  function blocks(p) {
    return SHAPES[p.type][p.rot];
  }
  function collides(type, rot, x, y) {
    for (const [bx, by] of SHAPES[type][rot]) {
      const nx = x + bx, ny = y + by;
      if (nx < 0 || nx >= COLS || ny >= ROWS) return true;
      if (ny >= 0 && state.board[ny][nx]) return true;
    }
    return false;
  }
  function grounded() {
    const p = state.piece;
    return !!p && collides(p.type, p.rot, p.x, p.y + 1);
  }
  function ghostY() {
    const p = state.piece;
    if (!p) return 0;
    let y = p.y;
    while (!collides(p.type, p.rot, p.x, y + 1)) y++;
    return y;
  }
  function baseGravityMs() {
    return Math.max(90, 750 - (state.level - 1) * 45);
  }
  function gravityMs() {
    return state.soft ? SOFT_MS : baseGravityMs();
  }

  // ═══ 渲染 ═══
  function newCellEl(id, x, y, type) {
    let cell = state.cellEls.get(id);
    if (!cell) {
      cell = document.createElement('div');
      cell.className = 'board-cell';
      state.cellEls.set(id, cell);
      el.stack.appendChild(cell);
    }
    cell.className = 'board-cell t-' + type + ' land';
    cell.style.left = (x * 10) + '%';
    cell.style.top = (y * 5) + '%';
    return cell;
  }

  function renderPiece() {
    const p = state.piece;
    if (!p) {
      el.pieceGroup.style.display = 'none';
      el.ghostGroup.style.display = 'none';
      return;
    }
    el.pieceGroup.style.display = 'block';
    el.pieceGroup.className = 'piece-group';
    el.pieceGroup.style.transform =
      'translate(' + (p.x * 25) + '%, ' + (p.y * 25) + '%)';
    el.pieceGroup.innerHTML = '';
    for (const [bx, by] of blocks(p)) {
      const b = document.createElement('div');
      b.className = 'piece-block t-' + p.type;
      b.style.left = (bx * 25) + '%';
      b.style.top = (by * 25) + '%';
      el.pieceGroup.appendChild(b);
    }

    const gy = ghostY();
    el.ghostGroup.style.display = 'block';
    el.ghostGroup.className = 'ghost-group ghost-' + p.type;
    el.ghostGroup.style.transform =
      'translate(' + (p.x * 25) + '%, ' + (gy * 25) + '%)';
    el.ghostGroup.innerHTML = '';
    for (const [bx, by] of blocks(p)) {
      const b = document.createElement('div');
      b.className = 'ghost-block';
      b.style.left = (bx * 25) + '%';
      b.style.top = (by * 25) + '%';
      el.ghostGroup.appendChild(b);
    }
  }

  function renderMiniPiece(box, type) {
    box.innerHTML = '';
    if (!type) return;
    const shape = SHAPES[type][0];
    const xs = shape.map(function (b) { return b[0]; });
    const ys = shape.map(function (b) { return b[1]; });
    const minX = Math.min.apply(null, xs);
    const maxX = Math.max.apply(null, xs);
    const minY = Math.min.apply(null, ys);
    const maxY = Math.max.apply(null, ys);
    const w = maxX - minX + 1, h = maxY - minY + 1;
    for (const [bx, by] of shape) {
      const b = document.createElement('div');
      b.className = 'mini-block t-' + type;
      b.style.left = ((bx - minX) / w * 100) + '%';
      b.style.top = ((by - minY) / h * 100) + '%';
      b.style.width = (100 / w) + '%';
      b.style.height = (100 / h) + '%';
      box.appendChild(b);
    }
  }

  function renderNext() {
    el.nextBoxes.innerHTML = '';
    preview(3).forEach(function (type) {
      const box = document.createElement('div');
      box.className = 'mini-box';
      renderMiniPiece(box, type);
      el.nextBoxes.appendChild(box);
    });
  }
  function renderHold() {
    el.holdBox.innerHTML = '';
    renderMiniPiece(el.holdBox, state.hold);
  }

  function updateHud() {
    el.score.textContent = state.score;
    el.lines.textContent = state.lines;
    el.level.textContent = state.level;
  }

  // ── 观战区 ──
  // 全量棋盘快照渲染：将来由服务器推送其他玩家的数据，函数直接复用。
  function renderMiniBoard(slot, snap) {
    slot.innerHTML = '';
    const board = document.createElement('div');
    board.className = 'mini-board' + (snap.alive ? '' : ' waiting');
    for (let y = 0; y < ROWS; y++) {
      for (let x = 0; x < COLS; x++) {
        const c = document.createElement('div');
        const t = snap.cells[y * COLS + x];
        c.className = 'mini-cell' + (t ? ' t-' + TYPES[t - 1] : '');
        board.appendChild(c);
      }
    }
    const name = document.createElement('div');
    name.className = 'mini-name';
    name.textContent = snap.name;
    slot.appendChild(board);
    slot.appendChild(name);
  }

  function mockSnapshot(seed, name) {
    const cells = new Array(ROWS * COLS).fill(0);
    let s = seed;
    const rnd = function () {
      s = (s * 1103515245 + 12345) & 0x7fffffff;
      return s / 0x7fffffff;
    };
    for (let y = ROWS - 8; y < ROWS; y++) {
      for (let x = 0; x < COLS; x++) {
        cells[y * COLS + x] = rnd() < 0.45 ? (1 + ((rnd() * 7) | 0)) : 0;
      }
    }
    return { name: name, alive: true, cells: cells };
  }

  // 观战数据来源：接服务器后替换为服务器推送的其他玩家快照。
  function getOpponentSlots() {
    return [
      mockSnapshot(20240721, '演示·玩家A'),
      mockSnapshot(9876543, '演示·玩家B'),
      { name: '等待接入…', alive: false, cells: new Array(ROWS * COLS).fill(0) },
      { name: '等待接入…', alive: false, cells: new Array(ROWS * COLS).fill(0) },
    ];
  }

  function renderSpectate() {
    const slots = getOpponentSlots();
    el.spectateGrid.innerHTML = '';
    slots.forEach(function (snap) {
      const slot = document.createElement('div');
      slot.className = 'spectate-slot';
      renderMiniBoard(slot, snap);
      el.spectateGrid.appendChild(slot);
    });
  }

  // ═══ 游戏流程 ═══
  function showOverlay(id) { el.overlays[id].classList.remove('hidden'); }
  function hideOverlay(id) { el.overlays[id].classList.add('hidden'); }
  function hideAllOverlays() {
    ['ready', 'pause', 'over'].forEach(hideOverlay);
  }

  function resetBoard() {
    state.board = [];
    for (let y = 0; y < ROWS; y++) state.board.push(new Array(COLS).fill(null));
    state.cellEls.forEach(function (cell) { cell.remove(); });
    state.cellEls.clear();
    state.cellSeq = 0;
    el.stack.innerHTML = '';
  }

  function clearTimers() {
    clearTimeout(state.gravityTimer);
    clearTimeout(state.lockTimer);
    clearTimeout(state.leftTimer);
    clearInterval(state.leftInterval);
    clearTimeout(state.rightTimer);
    clearInterval(state.rightInterval);
    state.gravityTimer = state.lockTimer = null;
    state.leftTimer = state.rightTimer = null;
    state.leftInterval = state.rightInterval = null;
  }

  function resetRun() {
    clearTimers();
    resetBoard();
    state.piece = null;
    state.hold = null;
    state.holdUsed = false;
    state.bag = shuffleBag();
    state.score = 0;
    state.lines = 0;
    state.level = 1;
    state.soft = false;
    state.lockResets = 0;
    state.clearingRows = [];
    hideAllOverlays();
    renderPiece();
    renderNext();
    renderHold();
    updateHud();
  }

  function spawnNext() {
    const type = nextType();
    // 出生在可见区上方一行（y=-1）：方块自上而下入场，最上面一行能被充分利用；
    // 仅当出生位置的可见格被占满时判负（I 等一行高的方块仍可放进仅剩的一行）
    if (collides(type, 0, 3, -1)) {
      gameOver();
      return;
    }
    state.piece = { type: type, rot: 0, x: 3, y: -1 };
    state.holdUsed = false;
    renderPiece();
    renderNext();
    scheduleGravity();
  }

  function scheduleGravity() {
    clearTimeout(state.gravityTimer);
    state.gravityTimer = null;
    if (state.phase !== 'playing' || state.lockTimer) return;
    state.gravityTimer = setTimeout(gravityStep, gravityMs());
  }

  function gravityStep() {
    if (state.phase !== 'playing') return;
    if (!grounded()) {
      state.piece.y++;
      renderPiece();
      scheduleGravity();
    } else {
      beginLock();
    }
  }

  function beginLock() {
    if (state.lockTimer) return;
    state.lockResets = 0;
    state.lockTimer = setTimeout(lockPiece, LOCK_DELAY_MS);
  }

  function resetLock() {
    if (!state.lockTimer) return;
    if (state.lockResets >= LOCK_RESET_MAX) return;
    state.lockResets++;
    clearTimeout(state.lockTimer);
    state.lockTimer = setTimeout(lockPiece, LOCK_DELAY_MS);
  }

  function cancelLock() {
    clearTimeout(state.lockTimer);
    state.lockTimer = null;
  }

  function afterAction() {
    if (state.phase !== 'playing') return;
    if (grounded()) {
      if (state.lockTimer) resetLock();
      else beginLock();
    } else {
      cancelLock();
      scheduleGravity();
    }
  }

  function lockPiece() {
    cancelLock();
    const p = state.piece;
    if (!p) return;
    for (const [bx, by] of blocks(p)) {
      if (by + p.y < 0) { gameOver(); return; }
      const x = p.x + bx, y = p.y + by;
      const id = ++state.cellSeq;
      state.board[y][x] = { t: p.type, id: id };
      newCellEl(id, x, y, p.type);
    }
    state.piece = null;
    renderPiece();

    const full = [];
    for (let y = 0; y < ROWS; y++) {
      if (state.board[y].every(function (c) { return c; })) full.push(y);
    }
    if (full.length) startClear(full);
    else spawnNext();
  }

  function startClear(rows) {
    state.phase = 'clearing';
    clearTimers();
    state.clearingRows = rows;
    for (const y of rows) {
      for (let x = 0; x < COLS; x++) {
        const cell = state.board[y][x];
        const cEl = state.cellEls.get(cell.id);
        if (cEl) cEl.classList.add('row-flash');
      }
    }
    setTimeout(finishClear, FLASH_MS);
  }

  function finishClear() {
    const rows = state.clearingRows;
    const clearSet = new Set(rows);
    for (const y of rows) {
      for (let x = 0; x < COLS; x++) {
        const cell = state.board[y][x];
        const cEl = state.cellEls.get(cell.id);
        if (cEl) cEl.remove();
        state.cellEls.delete(cell.id);
      }
    }
    // 从下往上压缩：非清除行下移（CSS transition 自动播放塌落动画）
    let writeY = ROWS - 1;
    for (let y = ROWS - 1; y >= 0; y--) {
      if (clearSet.has(y)) continue;
      const row = state.board[y];
      for (let x = 0; x < COLS; x++) {
        const cell = row[x];
        if (cell) {
          const cEl = state.cellEls.get(cell.id);
          if (cEl) cEl.style.top = (writeY * 5) + '%';
        }
      }
      state.board[writeY] = row;
      writeY--;
    }
    for (; writeY >= 0; writeY--) {
      state.board[writeY] = new Array(COLS).fill(null);
    }

    const n = rows.length;
    state.lines += n;
    state.score += SCORE_TABLE[n] * state.level;
    state.level = Math.floor(state.lines / 10) + 1;
    updateHud();

    // 攻击钩子占位：将来在此根据消除行数随机选择目标玩家发动攻击
    // onLineClear(n);

    setTimeout(function () {
      if (state.phase === 'clearing') {
        state.phase = 'playing';
        spawnNext();
      }
    }, COLLAPSE_MS);
  }

  function gameOver() {
    state.phase = 'over';
    clearTimers();
    state.piece = null;
    renderPiece();
    el.overStats.textContent =
      '得分 ' + state.score + ' · 消行 ' + state.lines + ' · 等级 ' + state.level;
    showOverlay('over');
  }

  function beginReady() {
    state.phase = 'ready';
    el.readyText.textContent = 'READY';
    showOverlay('ready');
    const seq = ['3', '2', '1', 'GO!'];
    let i = 0;
    setTimeout(function tick() {
      if (state.phase !== 'ready') return;
      if (i < seq.length) {
        el.readyText.textContent = seq[i++];
        setTimeout(tick, 550);
      } else {
        hideOverlay('ready');
        state.phase = 'playing';
        spawnNext();
      }
    }, 700);
  }

  function pause() {
    if (state.phase !== 'playing') return;
    state.phase = 'paused';
    clearTimers();
    showOverlay('pause');
  }
  function resume() {
    if (state.phase !== 'paused') return;
    state.phase = 'playing';
    hideOverlay('pause');
    if (grounded()) beginLock();
    else scheduleGravity();
  }

  // ═══ 输入 ═══
  function move(dx) {
    const p = state.piece;
    if (state.phase !== 'playing' || !p) return;
    if (!collides(p.type, p.rot, p.x + dx, p.y)) {
      p.x += dx;
      renderPiece();
      afterAction();
    }
  }

  function tryRotate(dir) {
    const p = state.piece;
    if (state.phase !== 'playing' || !p) return;
    const rot = (p.rot + dir + 4) % 4;
    const kicks = p.type === 'I' ? KICKS_I : KICKS_JLSTZ;
    const list = kicks[p.rot + '>' + rot];
    for (const [dx, dy] of list) {
      if (!collides(p.type, rot, p.x + dx, p.y + dy)) {
        p.x += dx;
        p.y += dy;
        p.rot = rot;
        renderPiece();
        afterAction();
        return;
      }
    }
  }

  function hardDrop() {
    const p = state.piece;
    if (state.phase !== 'playing' || !p) return;
    const dist = ghostY() - p.y;
    p.y = ghostY();
    state.score += dist * 2;
    updateHud();
    // 硬降直接到位（临时关闭过渡），锁定后由 cellLand 动画给出落锁反馈
    el.pieceGroup.style.transition = 'none';
    renderPiece();
    void el.pieceGroup.offsetWidth;
    el.pieceGroup.style.transition = '';
    lockPiece();
  }

  function doHold() {
    if (state.phase !== 'playing' || !state.piece || state.holdUsed) return;
    const cur = state.piece.type;
    state.piece = null;
    renderPiece();
    if (state.hold) {
      const type = state.hold;
      state.hold = cur;
      state.piece = { type: type, rot: 0, x: 3, y: -1 };
      if (collides(type, 0, 3, -1)) {
        gameOver();
        return;
      }
    } else {
      state.hold = cur;
      spawnNext();
    }
    state.holdUsed = true;
    renderHold();
    renderPiece();
    cancelLock();
    scheduleGravity();
  }

  function setSoft(on) {
    if (state.soft === on) return;
    state.soft = on;
    if (state.phase === 'playing' && !state.lockTimer) scheduleGravity();
  }

  function pressLeft() {
    if (state.leftTimer || state.leftInterval) return;
    move(-1);
    state.leftTimer = setTimeout(function () {
      state.leftTimer = null;
      state.leftInterval = setInterval(function () { move(-1); }, ARR_MS);
    }, DAS_MS);
  }
  function releaseLeft() {
    clearTimeout(state.leftTimer);
    clearInterval(state.leftInterval);
    state.leftTimer = state.leftInterval = null;
  }
  function pressRight() {
    if (state.rightTimer || state.rightInterval) return;
    move(1);
    state.rightTimer = setTimeout(function () {
      state.rightTimer = null;
      state.rightInterval = setInterval(function () { move(1); }, ARR_MS);
    }, DAS_MS);
  }
  function releaseRight() {
    clearTimeout(state.rightTimer);
    clearInterval(state.rightInterval);
    state.rightTimer = state.rightInterval = null;
  }

  function onKeyDown(e) {
    if (!state.active) return;
    switch (e.code) {
      case 'ArrowLeft': e.preventDefault(); pressLeft(); break;
      case 'ArrowRight': e.preventDefault(); pressRight(); break;
      case 'ArrowDown': e.preventDefault(); if (!e.repeat) setSoft(true); break;
      case 'ArrowUp': case 'KeyX': e.preventDefault(); tryRotate(1); break;
      case 'KeyZ': tryRotate(-1); break;
      case 'Space': e.preventDefault(); hardDrop(); break;
      case 'KeyC': case 'ShiftLeft': case 'ShiftRight': doHold(); break;
      case 'KeyP': case 'Escape':
        if (state.phase === 'playing') pause();
        else if (state.phase === 'paused') resume();
        break;
    }
  }
  function onKeyUp(e) {
    switch (e.code) {
      case 'ArrowLeft': releaseLeft(); break;
      case 'ArrowRight': releaseRight(); break;
      case 'ArrowDown': setSoft(false); break;
    }
  }
  function onBlur() {
    if (state.phase === 'playing') pause();
  }

  // ═══ 触屏按钮 ═══
  function bindTouch(id, downFn, upFn) {
    const btn = $(id);
    if (!btn) return;
    btn.addEventListener('pointerdown', function (e) {
      e.preventDefault();
      if (btn.setPointerCapture) btn.setPointerCapture(e.pointerId);
      downFn();
    });
    if (upFn) {
      btn.addEventListener('pointerup', upFn);
      btn.addEventListener('pointercancel', upFn);
    }
  }

  // ═══ 对外接口 ═══
  function blurActive() {
    // 点击按钮后移出焦点，避免游戏内按 Enter/Space 误触发按钮
    if (document.activeElement && document.activeElement.blur) document.activeElement.blur();
  }

  function start() {
    blurActive();
    state.active = true;
    resetRun();
    renderSpectate();
    showView('game');
    beginReady();
  }

  function stop() {
    state.active = false;
    clearTimers();
    state.phase = 'idle';
    state.soft = false;
    hideAllOverlays();
  }

  function restart() {
    blurActive();
    if (!state.active) return;
    resetRun();
    renderSpectate();
    beginReady();
  }

  // ═══ 初始化 ═══
  function initDom() {
    el.stack = $('boardStack');
    el.pieceGroup = $('pieceGroup');
    el.ghostGroup = $('ghostGroup');
    el.holdBox = $('holdBox');
    el.nextBoxes = $('nextBoxes');
    el.score = $('gameScore');
    el.lines = $('gameLines');
    el.level = $('gameLevel');
    el.readyText = $('readyText');
    el.overStats = $('overStats');
    el.spectateGrid = $('spectateGrid');
    el.overlays = { ready: $('overlayReady'), pause: $('overlayPause'), over: $('overlayOver') };
    el.pieceGroup.classList.add('piece-group');
    el.ghostGroup.classList.add('ghost-group');
  }

  function init() {
    initDom();

    const startBtn = $('btnStart');
    if (startBtn) {
      startBtn.disabled = false;
      startBtn.textContent = '开始游戏';
      startBtn.addEventListener('click', start);
    }
    const backBtn = $('btnBackLobby');
    if (backBtn) {
      backBtn.addEventListener('click', function () {
        stop();
        showView('lobby');
      });
    }
    const restartBtn = $('btnRestart');
    if (restartBtn) restartBtn.addEventListener('click', restart);
    const pauseBtn = $('btnPause');
    if (pauseBtn) pauseBtn.addEventListener('click', pause);
    const resumeBtn = $('btnResume');
    if (resumeBtn) resumeBtn.addEventListener('click', resume);
    const againBtn = $('btnAgain');
    if (againBtn) againBtn.addEventListener('click', restart);
    const quitPauseBtn = $('btnQuitPause');
    if (quitPauseBtn) {
      quitPauseBtn.addEventListener('click', function () {
        stop();
        showView('lobby');
      });
    }
    const quitOverBtn = $('btnQuitOver');
    if (quitOverBtn) {
      quitOverBtn.addEventListener('click', function () {
        stop();
        showView('lobby');
      });
    }

    bindTouch('touchLeft', pressLeft, releaseLeft);
    bindTouch('touchRight', pressRight, releaseRight);
    bindTouch('touchRotate', function () { tryRotate(1); }, null);
    bindTouch('touchDrop', hardDrop, null);
    bindTouch('touchDown', function () { setSoft(true); }, function () { setSoft(false); });

    window.addEventListener('keydown', onKeyDown);
    window.addEventListener('keyup', onKeyUp);
    window.addEventListener('blur', onBlur);

    resetRun();
  }

  init();

  // 全量棋盘快照（后续与服务器做全量同步时使用）
  function snapshot() {
    const cells = new Array(ROWS * COLS).fill(0);
    for (let y = 0; y < ROWS; y++) {
      for (let x = 0; x < COLS; x++) {
        const c = state.board[y][x];
        if (c) cells[y * COLS + x] = TYPES.indexOf(c.t) + 1;
      }
    }
    return { w: COLS, h: ROWS, cells: cells, lines: state.lines, score: state.score };
  }

  // 攻击/垃圾行钩子（后续接入服务器对战）
  // receiveGarbage(rows)：被攻击时在底部插入垃圾行
  // onLineClear(lines)：消行时在此随机选择目标玩家发动攻击

  window.TetrisGame = {
    start: start,
    stop: stop,
    restart: restart,
    pause: pause,
    resume: resume,
    snapshot: snapshot,
    renderMiniBoard: renderMiniBoard,
  };
})();
