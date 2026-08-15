"use strict";

// ═══════════════════════════════════════════════════════════════
// 协议编解码：protobuf wire 信封 + 业务消息体
// 信封 Msg：1=msg_type 2=msg_id 3=uid 4=token 5=cmd 6=body
// int64（uid/雪花 ID）用 BigInt 处理，避免 JS Number 精度丢失
// ═══════════════════════════════════════════════════════════════

const te = new TextEncoder();
const td = new TextDecoder('utf-8', { fatal: true });

function concatBytes(parts) {
  let total = 0;
  for (const p of parts) total += p.length;
  const out = new Uint8Array(total);
  let off = 0;
  for (const p of parts) { out.set(p, off); off += p.length; }
  return out;
}

// varint 编码：兼容 Number / BigInt / 十进制字符串
function varintBytes(n) {
  let v;
  if (typeof n === 'bigint') v = n;
  else if (typeof n === 'string') v = BigInt(n);
  else v = BigInt(Math.trunc(Number(n)));
  if (v < 0n) throw new Error('varint 不支持负数: ' + v);
  const out = [];
  while (v >= 0x80n) {
    out.push(Number(v & 0x7fn) | 0x80);
    v >>= 7n;
  }
  out.push(Number(v));
  return Uint8Array.from(out);
}

// varint 解码：安全范围内的值返回 Number，超出返回 BigInt
function readVarint(u8, pos) {
  let value = 0n;
  let shift = 0n;
  for (let i = 0; i < 10; i++) {
    if (pos + i >= u8.length) throw new Error('varint 数据截断');
    const b = u8[pos + i];
    value += BigInt(b & 0x7f) << shift;
    if ((b & 0x80) === 0) {
      if (value <= BigInt(Number.MAX_SAFE_INTEGER)) {
        return { value: Number(value), pos: pos + i + 1 };
      }
      return { value, pos: pos + i + 1 };
    }
    shift += 7n;
  }
  throw new Error('varint 超过 10 字节');
}

function tagBytes(field, wire) {
  return varintBytes((BigInt(field) << 3n) | BigInt(wire));
}
function bytesField(u8) { return concatBytes([varintBytes(u8.length), u8]); }
function strFieldBytes(s) { return bytesField(te.encode(s)); }

function readBytes(u8, pos) {
  const len = readVarint(u8, pos);
  const end = len.pos + Number(len.value);
  if (end > u8.length) throw new Error('bytes 数据截断');
  return { bytes: u8.slice(len.pos, end), pos: end };
}

function readString(u8, pos) {
  const r = readBytes(u8, pos);
  return { value: td.decode(r.bytes), pos: r.pos };
}

function skipField(u8, pos, wire) {
  if (wire === 0) return readVarint(u8, pos).pos;
  if (wire === 1) { if (pos + 8 > u8.length) throw new Error('fixed64 截断'); return pos + 8; }
  if (wire === 2) return readBytes(u8, pos).pos;
  if (wire === 5) { if (pos + 4 > u8.length) throw new Error('fixed32 截断'); return pos + 4; }
  throw new Error('未知 wire type: ' + wire);
}

// ── 业务消息体 schema：字段号 → [字段名, 类型, 子消息类型] ──
const MSGS = {
  LoginReq: { 1: ['UUid', 'varint'], 2: ['Name', 'string'], 3: ['PassWord', 'string'] },
  LoginRes: { 1: ['UUid', 'varint'], 2: ['Code', 'varint'] },
  HeartReq: {},
  HeartRes: { 1: ['Time', 'varint'] },
  ErrorRes: { 1: ['Code', 'varint'], 2: ['Msg', 'string'] },
  SendChatReq: { 1: ['UUid', 'varint'], 2: ['Msg', 'string'] },
  SendChatRsp: { 1: ['UUid', 'varint'] },
  ChatNtf: { 1: ['UUid', 'varint'], 2: ['Msg', 'string'], 3: ['Name', 'string'], 4: ['Time', 'varint'] },
  ChatHistoryReq: {},
  ChatHistoryRes: { 1: ['Messages', 'msg[]', 'ChatNtf'] },
  LobbyInfoReq: {},
  LobbyInfoRes: { 1: ['Online', 'varint'], 2: ['Players', 'msg[]', 'LobbyPlayer'] },
  LobbyPlayer: { 1: ['UID', 'varint'], 2: ['Name', 'string'] },
  LobbyJoinPush: { 1: ['Player', 'msg', 'LobbyPlayer'], 2: ['Online', 'varint'] },
  LobbyLeavePush: { 1: ['Player', 'msg', 'LobbyPlayer'], 2: ['Online', 'varint'] },
  PlayerInfoReq: { 1: ['Name', 'string'] },
  PlayerInfoRes: { 1: ['UUid', 'varint'], 2: ['Name', 'string'], 3: ['Online', 'varint'], 4: ['LastLoginAt', 'varint'] },
};

// 命令号登记表
const CMDS = {
  1: { name: 'CmdLogin', req: 'LoginReq', rsp: 'LoginRes' },
  2: { name: 'CmdLoginRsp', rsp: 'LoginRes' },
  3: { name: 'CmdHeart', req: 'HeartReq', rsp: 'HeartRes' },
  4: { name: 'CmdHeartRsp', rsp: 'HeartRes' },
  5: { name: 'CmdSendChat', req: 'SendChatReq', rsp: 'SendChatRsp' },
  6: { name: 'CmdSendChatRsp', rsp: 'SendChatRsp' },
  7: { name: 'CmdLobbyInfo', req: 'LobbyInfoReq', rsp: 'LobbyInfoRes' },
  8: { name: 'CmdLobbyInfoRsp', rsp: 'LobbyInfoRes' },
  9: { name: 'CmdChatHistory', req: 'ChatHistoryReq', rsp: 'ChatHistoryRes' },
  10: { name: 'CmdChatHistoryRsp', rsp: 'ChatHistoryRes' },
  11: { name: 'CmdPlayerInfo', req: 'PlayerInfoReq', rsp: 'PlayerInfoRes' },
  12: { name: 'CmdPlayerInfoRsp', rsp: 'PlayerInfoRes' },
  10000: { name: 'CmdChatPush', push: 'ChatNtf' },
  10001: { name: 'CmdLobbyJoinPush', push: 'LobbyJoinPush' },
  10002: { name: 'CmdLobbyLeavePush', push: 'LobbyLeavePush' },
  65535: { name: 'CmdError', rsp: 'ErrorRes' },
};

function sortedFields(spec) {
  return Object.keys(spec).map(Number).sort((a, b) => a - b);
}

function encodeMessage(typeName, obj) {
  const spec = MSGS[typeName];
  if (!spec) throw new Error('未知消息体类型: ' + typeName);
  const parts = [];
  for (const fld of sortedFields(spec)) {
    const def = spec[fld];
    const key = def[0], kind = def[1], child = def[2];
    const v = obj ? obj[key] : undefined;
    if (v === undefined || v === null || v === '') continue;
    if (kind === 'varint') {
      parts.push(tagBytes(fld, 0), varintBytes(v));
    } else if (kind === 'string') {
      parts.push(tagBytes(fld, 2), strFieldBytes(String(v)));
    } else if (kind === 'msg') {
      parts.push(tagBytes(fld, 2), bytesField(encodeMessage(child, v)));
    } else if (kind === 'msg[]') {
      for (const item of v) {
        parts.push(tagBytes(fld, 2), bytesField(encodeMessage(child, item)));
      }
    } else {
      throw new Error('未知字段类型: ' + kind);
    }
  }
  return concatBytes(parts);
}

function decodeMessage(typeName, u8) {
  const spec = MSGS[typeName];
  if (!spec) throw new Error('未知消息体类型: ' + typeName);
  const out = {};
  let pos = 0;
  while (pos < u8.length) {
    const tag = readVarint(u8, pos);
    pos = tag.pos;
    const tv = BigInt(tag.value);
    const field = Number(tv >> 3n);
    const wire = Number(tv & 7n);
    const def = spec[field];
    if (!def) { pos = skipField(u8, pos, wire); continue; }
    const key = def[0], kind = def[1], child = def[2];
    if (kind === 'varint') {
      if (wire !== 0) throw new Error('字段 ' + field + ' 期望 varint');
      const v = readVarint(u8, pos); pos = v.pos; out[key] = v.value;
    } else if (kind === 'string') {
      if (wire !== 2) throw new Error('字段 ' + field + ' 期望 string');
      const v = readString(u8, pos); pos = v.pos; out[key] = v.value;
    } else if (kind === 'msg') {
      if (wire !== 2) throw new Error('字段 ' + field + ' 期望 message');
      const v = readBytes(u8, pos); pos = v.pos;
      out[key] = decodeMessage(child, v.bytes);
    } else if (kind === 'msg[]') {
      if (wire !== 2) throw new Error('字段 ' + field + ' 期望 repeated message');
      const v = readBytes(u8, pos); pos = v.pos;
      if (!out[key]) out[key] = [];
      out[key].push(decodeMessage(child, v.bytes));
    }
  }
  return out;
}

// ── 信封编解码 ──
function encodeEnvelope(m) {
  const parts = [];
  if (m.msgType) parts.push(tagBytes(1, 0), varintBytes(m.msgType));
  if (m.msgID) parts.push(tagBytes(2, 0), varintBytes(m.msgID));
  if (m.uid !== undefined && m.uid !== null && m.uid !== 0 && m.uid !== 0n) {
    parts.push(tagBytes(3, 0), varintBytes(m.uid));
  }
  if (m.token) parts.push(tagBytes(4, 2), strFieldBytes(m.token));
  if (m.cmd) parts.push(tagBytes(5, 0), varintBytes(m.cmd));
  if (m.body && m.body.length) parts.push(tagBytes(6, 2), bytesField(m.body));
  return concatBytes(parts);
}

function decodeEnvelope(u8) {
  const m = { msgType: 0, msgID: 0, uid: 0n, token: '', cmd: 0, body: new Uint8Array(0) };
  let pos = 0;
  while (pos < u8.length) {
    const tag = readVarint(u8, pos);
    pos = tag.pos;
    const tv = BigInt(tag.value);
    const field = Number(tv >> 3n);
    const wire = Number(tv & 7n);
    switch (field) {
      case 1: {
        if (wire !== 0) throw new Error('字段1 期望 varint');
        const v = readVarint(u8, pos); pos = v.pos; m.msgType = Number(v.value); break;
      }
      case 2: {
        if (wire !== 0) throw new Error('字段2 期望 varint');
        const v = readVarint(u8, pos); pos = v.pos; m.msgID = Number(v.value); break;
      }
      case 3: {
        if (wire !== 0) throw new Error('字段3 期望 varint');
        const v = readVarint(u8, pos); pos = v.pos; m.uid = v.value; break;
      }
      case 4: {
        if (wire !== 2) throw new Error('字段4 期望 string');
        const v = readString(u8, pos); pos = v.pos; m.token = v.value; break;
      }
      case 5: {
        if (wire !== 0) throw new Error('字段5 期望 varint');
        const v = readVarint(u8, pos); pos = v.pos; m.cmd = Number(v.value); break;
      }
      case 6: {
        if (wire !== 2) throw new Error('字段6 期望 bytes');
        const v = readBytes(u8, pos); pos = v.pos; m.body = v.bytes; break;
      }
      default: pos = skipField(u8, pos, wire);
    }
  }
  return m;
}

function bodyTypeFor(cmd, msgType) {
  const c = CMDS[cmd];
  if (!c) return null;
  if (msgType === 2) return c.rsp || null;
  if (msgType === 3) return c.push || null;
  return c.req || null;
}

function decodeBodyFor(cmd, msgType, body) {
  const typeName = bodyTypeFor(cmd, msgType);
  if (!typeName) return null;
  return decodeMessage(typeName, body);
}

// ── BigInt 安全 JSON 解析（JWT payload 含 int64 uid） ──
function parseJsonBigInt(text) {
  let i = 0;
  const n = text.length;

  function skipWs() {
    while (i < n && (text[i] === ' ' || text[i] === '\t' || text[i] === '\n' || text[i] === '\r')) i++;
  }
  function fail(msg) { throw new Error('JSON 解析失败: ' + msg + ' @' + i); }

  function expect(word) {
    if (text.substr(i, word.length) !== word) fail('期望 ' + word);
    i += word.length;
  }

  function parseValue() {
    skipWs();
    if (i >= n) fail('意外结束');
    const c = text[i];
    if (c === '{') return parseObject();
    if (c === '[') return parseArray();
    if (c === '"') return parseString();
    if (c === 't') { expect('true'); return true; }
    if (c === 'f') { expect('false'); return false; }
    if (c === 'n') { expect('null'); return null; }
    return parseNumber();
  }

  function parseObject() {
    i++;
    const out = {};
    skipWs();
    if (text[i] === '}') { i++; return out; }
    for (;;) {
      skipWs();
      if (text[i] !== '"') fail('期望字符串键');
      const key = parseString();
      skipWs();
      if (text[i] !== ':') fail('期望冒号');
      i++;
      out[key] = parseValue();
      skipWs();
      if (text[i] === ',') { i++; continue; }
      if (text[i] === '}') { i++; return out; }
      fail('期望 , 或 }');
    }
  }

  function parseArray() {
    i++;
    const out = [];
    skipWs();
    if (text[i] === ']') { i++; return out; }
    for (;;) {
      out.push(parseValue());
      skipWs();
      if (text[i] === ',') { i++; continue; }
      if (text[i] === ']') { i++; return out; }
      fail('期望 , 或 ]');
    }
  }

  function parseString() {
    i++;
    let s = '';
    while (i < n) {
      const c = text[i];
      if (c === '"') { i++; return s; }
      if (c === '\\') {
        i++;
        const e = text[i];
        if (e === 'n') s += '\n';
        else if (e === 't') s += '\t';
        else if (e === 'r') s += '\r';
        else if (e === 'b') s += '\b';
        else if (e === 'f') s += '\f';
        else if (e === '/' || e === '\\' || e === '"') s += e;
        else if (e === 'u') {
          const hex = text.substr(i + 1, 4);
          const cp = parseInt(hex, 16);
          if (isNaN(cp)) fail('非法 unicode 转义');
          s += String.fromCharCode(cp);
          i += 4;
        } else fail('非法转义 \\' + e);
        i++;
      } else {
        s += c;
        i++;
      }
    }
    fail('字符串未闭合');
  }

  function parseNumber() {
    const start = i;
    if (text[i] === '-') i++;
    while (i < n && /[0-9]/.test(text[i])) i++;
    let isFloat = false;
    if (i < n && text[i] === '.') {
      isFloat = true;
      i++;
      while (i < n && /[0-9]/.test(text[i])) i++;
    }
    if (i < n && (text[i] === 'e' || text[i] === 'E')) {
      isFloat = true;
      i++;
      if (text[i] === '+' || text[i] === '-') i++;
      while (i < n && /[0-9]/.test(text[i])) i++;
    }
    const raw = text.slice(start, i);
    if (raw === '' || raw === '-') fail('非法数字');
    if (!isFloat) {
      const big = BigInt(raw);
      if (big <= BigInt(Number.MAX_SAFE_INTEGER) && big >= BigInt(Number.MIN_SAFE_INTEGER)) {
        return Number(big);
      }
      return big;
    }
    return Number(raw);
  }

  const v = parseValue();
  skipWs();
  if (i !== n) fail('尾部多余内容');
  return v;
}

// 解码 JWT payload（base64url，int64 uid 保持精度）
function decodeJwtPayload(token) {
  try {
    const parts = String(token).split('.');
    if (parts.length !== 3) return null;
    const b64 = parts[1].replace(/-/g, '+').replace(/_/g, '/');
    const pad = (4 - (b64.length % 4)) % 4;
    const raw = atob(b64 + '='.repeat(pad));
    const bytes = Uint8Array.from(raw, c => c.charCodeAt(0));
    return parseJsonBigInt(td.decode(bytes));
  } catch (_) {
    return null;
  }
}

// BigInt 安全 JSON 序列化：日志等场景输出 int64（uid）时不抛错，
// BigInt 统一转为 "<值>n" 字符串，便于区分类型。
function safeStringify(value) {
  const seen = new WeakSet();
  return JSON.stringify(value, function (key, val) {
    if (typeof val === 'bigint') return val.toString() + 'n';
    if (typeof val === 'object' && val !== null) {
      if (seen.has(val)) return '[Circular]';
      seen.add(val);
    }
    return val;
  });
}
