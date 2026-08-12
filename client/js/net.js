"use strict";

// ═══════════════════════════════════════════════════════════════
// WsClient：连接 gateway、握手、按 msgID 匹配响应、3s 心跳、
// 断线自动重试 3 次（2s 间隔），之后交由应用层提示手动重连
// ═══════════════════════════════════════════════════════════════

class WsClient {
  constructor(opts) {
    this.opts = opts || {};
    this.ws = null;
    this.addr = '';
    this.token = '';
    this.uid = 0n;
    this.connected = false;
    this.handshaken = false;
    this.closedByUser = false;
    this.retries = 0;
    this.seq = 0;
    this.pending = new Map();
    this.hbTimer = null;
    this.readyResolve = null;
    this.readyReject = null;
    this.readyTimer = null;
  }

  connect(addr, token, uid) {
    this.addr = addr;
    this.token = token;
    this.uid = uid;
    this.closedByUser = false;
    this.retries = 0;
    return this.open();
  }

  open() {
    const url = /^wss?:\/\//i.test(this.addr) ? this.addr : 'ws://' + this.addr;
    this.emitStatus();
    this.readyPromise = new Promise((resolve, reject) => {
      this.readyResolve = resolve;
      this.readyReject = reject;
    });
    try {
      this.ws = new WebSocket(url);
    } catch (e) {
      this.readyReject(e);
      return Promise.reject(e);
    }
    this.ws.binaryType = 'arraybuffer';
    this.ws.onopen = () => {
      this.connected = true;
      this.emitStatus();
      this.sendHandshake();
      this.readyTimer = setTimeout(() => {
        this.readyReject(new Error('握手超时'));
        if (this.ws) { try { this.ws.close(4000, 'handshake timeout'); } catch (_) {} }
      }, 10000);
    };
    this.ws.onmessage = (ev) => { this.onMessage(ev); };
    this.ws.onerror = () => { /* 关闭事件统一处理 */ };
    this.ws.onclose = (ev) => { this.onClose(ev); };
    return this.readyPromise;
  }

  sendHandshake() {
    this.ws.send(JSON.stringify({ token: this.token, version: 1 }));
  }

  async onMessage(ev) {
    let data = ev.data;
    if (typeof data === 'string') {
      if (this.tryHandshakeText(data)) return;
      return;
    }
    if (data instanceof Blob) data = await data.arrayBuffer();
    const u8 = new Uint8Array(data);
    // gateway 的握手回执是二进制 JSON 帧
    try {
      if (this.tryHandshakeText(td.decode(u8))) return;
    } catch (_) { /* 非 UTF-8 文本，按协议信封处理 */ }
    try {
      this.handleFrame(u8);
    } catch (e) {
      if (this.opts.onFrameError) this.opts.onFrameError(e);
    }
  }

  tryHandshakeText(text) {
    let obj = null;
    try { obj = JSON.parse(text); } catch (_) { return false; }
    if (!obj || typeof obj !== 'object' || !obj.status) return false;
    if (obj.status === 'ok') {
      this.handshaken = true;
      if (this.readyTimer) { clearTimeout(this.readyTimer); this.readyTimer = null; }
      this.retries = 0;
      this.emitStatus();
      if (this.readyResolve) this.readyResolve();
      if (this.opts.onReady) this.opts.onReady();
    } else {
      const detail = obj.detail || 'handshake error';
      if (this.readyReject) this.readyReject(new Error('握手失败: ' + detail));
      if (this.opts.onHandshakeError) this.opts.onHandshakeError(detail);
    }
    return true;
  }

  handleFrame(u8) {
    const m = decodeEnvelope(u8);
    if (m.msgType === 2) {
      const entry = this.pending.get(m.msgID);
      if (!entry) return;
      this.pending.delete(m.msgID);
      clearTimeout(entry.timer);
      const body = decodeBodyFor(m.cmd, m.msgType, m.body) || {};
      if (m.cmd === 65535) {
        entry.reject(new Error('服务器错误: ' + (body.Msg || 'code ' + body.Code)));
        return;
      }
      entry.resolve(body);
      return;
    }
    if (m.msgType === 3) {
      const body = decodeBodyFor(m.cmd, m.msgType, m.body);
      if (this.opts.onPush) this.opts.onPush(m.cmd, body || {});
    }
  }

  sendReq(cmd, bodyType, bodyObj, opts) {
    if (!this.ws || this.ws.readyState !== WebSocket.OPEN) {
      return Promise.reject(new Error('未连接'));
    }
    const msgID = ++this.seq;
    const body = bodyType ? encodeMessage(bodyType, bodyObj || {}) : new Uint8Array(0);
    const bytes = encodeEnvelope({
      msgType: 1,
      msgID,
      uid: (opts && opts.uid !== undefined) ? opts.uid : this.uid,
      cmd,
      body,
    });
    this.ws.send(bytes);
    return new Promise((resolve, reject) => {
      const timer = setTimeout(() => {
        this.pending.delete(msgID);
        reject(new Error('请求超时 cmd=' + cmd));
      }, (opts && opts.timeoutMs) || 8000);
      this.pending.set(msgID, { cmd, resolve, reject, timer });
    });
  }

  startHeartbeat() {
    this.stopHeartbeat();
    this.hbTimer = setInterval(() => this.ping(), 3000);
    this.ping();
  }

  stopHeartbeat() {
    if (this.hbTimer) { clearInterval(this.hbTimer); this.hbTimer = null; }
  }

  ping() {
    this.sendReq(3, 'HeartReq', {}, { timeoutMs: 6000 })
      .then(() => { if (this.opts.onHeartbeat) this.opts.onHeartbeat(true); })
      .catch(() => { if (this.opts.onHeartbeat) this.opts.onHeartbeat(false); });
  }

  onClose(ev) {
    this.connected = false;
    this.handshaken = false;
    this.stopHeartbeat();
    if (this.readyTimer) { clearTimeout(this.readyTimer); this.readyTimer = null; }
    if (this.readyReject && !this.handshaken) {
      this.readyReject(new Error('连接已关闭'));
    }
    for (const entry of this.pending.values()) {
      clearTimeout(entry.timer);
      entry.reject(new Error('连接已断开'));
    }
    this.pending.clear();
    this.emitStatus();

    if (this.closedByUser) {
      if (this.opts.onClose) this.opts.onClose(ev.code, false, 0);
      return;
    }
    if (this.retries < 3) {
      this.retries++;
      if (this.opts.onClose) this.opts.onClose(ev.code, true, this.retries);
      setTimeout(() => {
        if (this.opts.onRetry) this.opts.onRetry(this.retries);
        this.open().catch(() => {});
      }, 2000);
    } else {
      if (this.opts.onClose) this.opts.onClose(ev.code, false, 3);
    }
  }

  close(manual) {
    this.closedByUser = !!manual;
    this.stopHeartbeat();
    if (this.ws) {
      try { this.ws.close(1000, 'bye'); } catch (_) {}
      this.ws = null;
    }
    this.connected = false;
    this.handshaken = false;
    this.emitStatus();
  }

  emitStatus() {
    if (this.opts.onStatus) this.opts.onStatus({ connected: this.connected, handshaken: this.handshaken });
  }
}
