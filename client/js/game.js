"use strict";

(function () {
  const BOARD_SIZE = 5;
  const EMPTY_CENTER = { r: 2, c: 2 };
  const SAVE_PREFIX = 'coffee_merge_game_';
  const LEVEL_IMAGES = {
    1: 'assets/chain_bean.png',
    2: 'assets/chain_green.png',
    3: 'assets/chain_roast.png',
    4: 'assets/chain_espresso.png',
    5: 'assets/chain_latte.png',
  };

  const game = {
    board: [],
    score: 0,
    selected: null,
  };

  function saveKey() {
    return SAVE_PREFIX + (state.account || 'anonymous');
  }

  function shuffle(values) {
    const arr = values.slice();
    for (let i = arr.length - 1; i > 0; i--) {
      const j = Math.floor(Math.random() * (i + 1));
      [arr[i], arr[j]] = [arr[j], arr[i]];
    }
    return arr;
  }

  function newBoard() {
    const board = Array.from({ length: BOARD_SIZE }, () => Array(BOARD_SIZE).fill(0));
    const values = [];
    for (let i = 0; i < 18; i++) values.push(1);
    for (let i = 0; i < 6; i++) values.push(2);
    const shuffled = shuffle(values);

    let index = 0;
    for (let r = 0; r < BOARD_SIZE; r++) {
      for (let c = 0; c < BOARD_SIZE; c++) {
        if (r === EMPTY_CENTER.r && c === EMPTY_CENTER.c) continue;
        board[r][c] = shuffled[index++];
      }
    }
    return board;
  }

  function loadSave() {
    try {
      const raw = localStorage.getItem(saveKey());
      if (!raw) return false;
      const data = JSON.parse(raw);
      if (!Array.isArray(data.board) || data.board.length !== BOARD_SIZE) return false;
      for (const row of data.board) {
        if (!Array.isArray(row) || row.length !== BOARD_SIZE) return false;
        if (row.some((v) => !Number.isInteger(v) || v < 0 || v > 5)) return false;
      }
      game.board = data.board;
      game.score = Number.isFinite(data.score) ? data.score : 0;
      return true;
    } catch (_) {
      return false;
    }
  }

  function persist() {
    localStorage.setItem(saveKey(), JSON.stringify({
      board: game.board,
      score: game.score,
    }));
  }

  function loadOrCreate() {
    if (!loadSave()) {
      game.board = newBoard();
      game.score = 0;
      persist();
    }
  }

  function startGame() {
    if (!state.account) {
      toast('请先登录');
      return;
    }
    game.selected = null;
    loadOrCreate();
    showView('game');
    render();
  }

  function backToLobby() {
    persist();
    game.selected = null;
    showView('lobby');
  }

  function restartGame() {
    if (!window.confirm('确定重新开始吗？当前棋盘会被覆盖。')) return;
    game.board = newBoard();
    game.score = 0;
    game.selected = null;
    persist();
    render();
  }

  function cellKey(r, c) {
    return r + ':' + c;
  }

  function isSelected(r, c) {
    return game.selected && game.selected.r === r && game.selected.c === c;
  }

  function renderScore() {
    const el = $('mergeScore');
    if (el) el.textContent = game.score;
  }

  function renderBoard() {
    const boardEl = $('mergeBoard');
    if (!boardEl) return;
    boardEl.innerHTML = '';

    for (let r = 0; r < BOARD_SIZE; r++) {
      for (let c = 0; c < BOARD_SIZE; c++) {
        const cell = document.createElement('button');
        cell.type = 'button';
        cell.className = 'merge-cell';
        cell.dataset.r = r;
        cell.dataset.c = c;
        cell.dataset.key = cellKey(r, c);

        const level = game.board[r][c];
        const img = document.createElement('img');
        img.draggable = false;
        if (level === 0) {
          cell.classList.add('empty');
          img.src = 'assets/cup.png';
          img.alt = '空位';
        } else {
          cell.classList.add('level-' + level);
          img.src = LEVEL_IMAGES[level];
          img.alt = '等级 ' + level;
        }
        if (isSelected(r, c)) cell.classList.add('selected');

        cell.appendChild(img);
        boardEl.appendChild(cell);
      }
    }
  }

  function render() {
    renderBoard();
    renderScore();
  }

  function merge(source, target) {
    const level = game.board[source.r][source.c];
    game.score += level * 20;

    if (level >= 5) {
      game.board[source.r][source.c] = 0;
      game.board[target.r][target.c] = 0;
      return;
    }

    game.board[source.r][source.c] = level + 1;
    game.board[target.r][target.c] = 0;
  }

  function onCellTap(r, c) {
    const level = game.board[r][c];

    if (level === 0) {
      game.board[r][c] = 1;
      game.selected = null;
      persist();
      render();
      return;
    }

    if (game.selected === null) {
      game.selected = { r, c };
      render();
      return;
    }

    if (game.selected.r === r && game.selected.c === c) {
      game.selected = null;
      render();
      return;
    }

    const selectedLevel = game.board[game.selected.r][game.selected.c];
    if (selectedLevel !== level) {
      game.selected = { r, c };
      render();
      return;
    }

    merge(game.selected, { r, c });
    game.selected = null;
    persist();
    render();
  }

  function handleBoardClick(event) {
    const cell = event.target.closest('.merge-cell');
    if (!cell) return;
    const r = Number(cell.dataset.r);
    const c = Number(cell.dataset.c);
    onCellTap(r, c);
  }

  function initGame() {
    const startBtn = $('btnStart');
    if (startBtn) {
      startBtn.disabled = false;
      startBtn.textContent = '开始游戏';
      startBtn.addEventListener('click', startGame);
    }

    const backBtn = $('btnBackLobby');
    if (backBtn) backBtn.addEventListener('click', backToLobby);

    const restartBtn = $('btnRestart');
    if (restartBtn) restartBtn.addEventListener('click', restartGame);

    const boardEl = $('mergeBoard');
    if (boardEl) boardEl.addEventListener('click', handleBoardClick);
  }

  initGame();
})();
