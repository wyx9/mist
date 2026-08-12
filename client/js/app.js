"use strict";

// ═══════════════════════════════════════════════════════════════
// 应用逻辑：登录流程、视图切换、大厅在线列表、状态与错误提示
// ═══════════════════════════════════════════════════════════════

const $ = (id) => document.getElementById(id);
const SESSION_KEY = 'coffee_merge_session';
const PASSWORD = '123';

const state = {
  account: '',
  token: '',
  uid: 0n,
  gateway: '',
  view: 'login',
  players: new Map(),
  online: 0,
};

const ws = new WsClient({
  onStatus: renderStatus,
  onReady: enterGameFlow,
  onPush: handlePush,
  onHeartbeat: renderHeartbeat,
  onClose: (code, willRetry, retries) => {
    if (state.view !== 'lobby') return;
    if (willRetry) {
      toast('连接断开（code=' + code + '），第 ' + retries + '/3 次重连…');
    } else {
      setPill('pillWs', 'WS 已断开', 'bad');
      setPill('pillHs', '未握手', 'bad');
      setPill('pillLogin', '未登录', 'bad');
      setPill('pillHb', '心跳 -', 'bad');
      toast('连接已断开，请退出后重新登录');
    }
  },
  onRetry: (n) => setPill('pillWs', 'WS 重连 ' + n + '/3', 'warn'),
  onFrameError: (e) => console.warn('[frame]', e.message),
});

function setPill(id, text, kind) {
  const el = $(id);
  el.textContent = text;
  el.className = 'pill ' + (kind || 'bad');
}

function renderStatus(st) {
  setPill('pillWs', st.connected ? 'WS 已连接' : 'WS 未连接', st.connected ? 'ok' : 'bad');
  setPill('pillHs', st.handshaken ? '已握手' : '未握手', st.handshaken ? 'ok' : 'bad');
}

function renderHeartbeat(ok) {
  setPill('pillHb', ok ? '心跳 OK' : '心跳 -', ok ? 'ok' : 'bad');
}

function showView(name) {
  state.view = name;
  $('view-login').classList.toggle('hidden', name !== 'login');
  $('view-lobby').classList.toggle('hidden', name !== 'lobby');
}

function showLoginError(msg) {
  const el = $('loginError');
  el.textContent = msg;
  el.classList.remove('hidden');
}

function setLoginBusy(busy) {
  const btn = $('btnLogin');
  btn.disabled = busy;
  btn.textContent = busy ? '登录中…' : '登 录';
}

let toastTimer = null;
function toast(msg) {
  const el = $('toast');
  el.textContent = msg;
  el.classList.remove('hidden');
  if (toastTimer) clearTimeout(toastTimer);
  toastTimer = setTimeout(() => el.classList.add('hidden'), 3500);
}

function saveSession() {
  localStorage.setItem(SESSION_KEY, JSON.stringify({
    account: state.account,
    token: state.token,
    gateway: state.gateway,
    uid: state.uid.toString(),
  }));
}

async function doLogin() {
  const account = $('accountInput').value.trim();
  if (!account) { showLoginError('请输入玩家账号'); return; }
  showLoginError('');
  setLoginBusy(true);
  const server = $('serverSel').value;
  try {
    const resp = await fetch('http://' + server + '/login', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ name: account, password: PASSWORD }),
    });
    const text = await resp.text();
    let obj = null;
    try { obj = JSON.parse(text); } catch (_) {}
    if (!resp.ok || !obj || !obj.token || !obj.gateway_addr) {
      throw new Error('登录失败（' + resp.status + '）: ' + (obj && obj.detail ? obj.detail : text));
    }
    const payload = decodeJwtPayload(obj.token);
    if (!payload || !payload.uid) throw new Error('token 缺少 uid');

    state.account = account;
    state.token = obj.token;
    state.gateway = obj.gateway_addr;
    state.uid = typeof payload.uid === 'bigint' ? payload.uid : BigInt(payload.uid);
    state.players.clear();
    state.online = 0;
    saveSession();
    enterLobbyView();
    ws.connect(state.gateway, state.token, state.uid).catch((e) => toast(e.message));
  } catch (e) {
    showLoginError(e.message);
  } finally {
    setLoginBusy(false);
  }
}

function enterLobbyView() {
  showView('lobby');
  $('lobbyName').textContent = state.account;
  $('lobbyUid').textContent = state.uid.toString();
  setPill('pillWs', 'WS 连接中…', 'warn');
  setPill('pillHs', '未握手', 'bad');
  setPill('pillLogin', '登录中…', 'warn');
  setPill('pillHb', '心跳 -', 'bad');
  renderPlayers();
}

async function enterGameFlow() {
  setPill('pillWs', 'WS 已连接', 'ok');
  setPill('pillHs', '已握手', 'ok');
  setPill('pillLogin', '登录中…', 'warn');
  try {
    const res = await ws.sendReq(1, 'LoginReq', {
      UUid: state.uid,
      Name: state.account,
      PassWord: PASSWORD,
    }, { uid: state.uid, timeoutMs: 8000 });
    if (res.Code !== 0) throw new Error('游戏登录失败 Code=' + res.Code);
    setPill('pillLogin', '已登录', 'ok');
    ws.startHeartbeat();
    await refreshLobby();
  } catch (e) {
    setPill('pillLogin', '登录失败', 'bad');
    toast(e.message);
  }
}

async function refreshLobby() {
  try {
    const res = await ws.sendReq(7, 'LobbyInfoReq', {}, { uid: state.uid, timeoutMs: 8000 });
    state.players.clear();
    for (const p of res.Players || []) {
      state.players.set(String(p.UID), { uid: p.UID, name: p.Name });
    }
    state.online = res.Online || 0;
    renderPlayers();
  } catch (e) {
    toast('获取在线列表失败: ' + e.message);
  }
}

function handlePush(cmd, body) {
  if (cmd === 10001) { // LobbyJoinPush
    if (body.Player && String(body.Player.UID) !== String(state.uid)) {
      state.players.set(String(body.Player.UID), { uid: body.Player.UID, name: body.Player.Name });
    }
    if (body.Online !== undefined && body.Online !== null) state.online = body.Online;
    renderPlayers();
  } else if (cmd === 10002) { // LobbyLeavePush
    if (body.Player) state.players.delete(String(body.Player.UID));
    if (body.Online !== undefined && body.Online !== null) state.online = body.Online;
    renderPlayers();
  }
}

function renderPlayers() {
  const list = Array.from(state.players.values());
  list.sort((a, b) => String(a.name).localeCompare(String(b.name), 'zh-CN'));
  const ul = $('playerList');
  ul.innerHTML = '';
  $('onlineCount').textContent = state.online;

  if (!list.length) {
    const li = document.createElement('li');
    li.className = 'empty';
    li.textContent = '暂无其他玩家在线';
    ul.appendChild(li);
    return;
  }
  for (const p of list) {
    const li = document.createElement('li');
    const img = document.createElement('img');
    img.src = 'assets/bean.png';
    img.alt = '';
    const name = document.createElement('span');
    name.textContent = p.name;
    li.appendChild(img);
    li.appendChild(name);
    if (String(p.uid) === String(state.uid)) {
      const me = document.createElement('span');
      me.className = 'me';
      me.textContent = '（我）';
      li.appendChild(me);
    }
    ul.appendChild(li);
  }
}

function logout() {
  ws.close(true);
  localStorage.removeItem(SESSION_KEY);
  state.players.clear();
  state.online = 0;
  showView('login');
  setPill('pillWs', 'WS 未连接', 'bad');
  setPill('pillHs', '未握手', 'bad');
  setPill('pillLogin', '未登录', 'bad');
  setPill('pillHb', '心跳 -', 'bad');
}

function restoreSession() {
  let saved = null;
  try { saved = JSON.parse(localStorage.getItem(SESSION_KEY)); } catch (_) {}
  if (!saved || !saved.account || !saved.token || !saved.gateway || !saved.uid) return;
  state.account = saved.account;
  state.token = saved.token;
  state.gateway = saved.gateway;
  state.uid = BigInt(saved.uid);
  enterLobbyView();
  toast('恢复会话，重新登录中…');
  ws.connect(state.gateway, state.token, state.uid).catch((e) => toast(e.message));
}

function init() {
  $('btnLogin').addEventListener('click', doLogin);
  $('accountInput').addEventListener('keydown', (e) => {
    if (e.key === 'Enter') doLogin();
  });
  $('btnLogout').addEventListener('click', logout);
  restoreSession();
}

init();
