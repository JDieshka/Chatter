// WebSocket-клиент: ws://{origin}/ws?token={access}
// Протокол сервера:
//   клиент -> сервер: {type:"chat.join"|"chat.leave"|"message.send"|"ping", chat_id, content}
//   сервер -> клиент: {type:"message.new", id, chat_id, sender_id, sender:{...}, content, created_at}
//                      {type:"error", code, message} | {type:"pong"}
const WS = {
  conn: null,
  connected: false,
  joinedChats: new Set(), // подписки на все чаты пользователя (не отписываемся при смене открытого чата)
  onMessage: null,    // callback(event)
  onStatus: null,     // callback(connected:boolean)
  onError: null,      // callback({code,message})
  _retry: 0,
  _closedByUser: false,
  _pingTimer: null,

  open() {
    if (!API.access) return;
    this._closedByUser = false;
    // не открываем дубликат соединения, если уже открыто/открывается
    if (this.conn && (this.conn.readyState === WebSocket.OPEN || this.conn.readyState === WebSocket.CONNECTING)) return;
    const proto = location.protocol === 'https:' ? 'wss:' : 'ws:';
    const url = proto + '//' + location.host + '/ws?token=' + encodeURIComponent(API.access);
    try {
      this.conn = new WebSocket(url);
    } catch (e) {
      this._scheduleReconnect();
      return;
    }

    this.conn.onopen = () => {
      this.connected = true;
      this._retry = 0;
      if (this.onStatus) this.onStatus(true);
      // переподписка на все известные чаты после реконнекта
      this.joinedChats.forEach(id => this.send({ type: 'chat.join', chat_id: id }));
      // keep-alive: не даём прокси/балансировщику убить «молчащее» соединение
      clearInterval(this._pingTimer);
      this._pingTimer = setInterval(() => this.send({ type: 'ping' }), 25000);
    };

    this.conn.onmessage = (ev) => {
      let data;
      try { data = JSON.parse(ev.data); } catch (e) { return; }
      if (data.type === 'error') {
        console.warn('WS error:', data.code, data.message);
        if (this.onError) this.onError(data);
        return;
      }
      if (data.type === 'pong') return;
      if (this.onMessage) this.onMessage(data);
    };

    this.conn.onclose = () => {
      this.connected = false;
      clearInterval(this._pingTimer);
      this._pingTimer = null;
      if (this.onStatus) this.onStatus(false);
      if (!this._closedByUser) this._scheduleReconnect();
    };

    this.conn.onerror = () => { /* onclose последует сам */ };
  },

  _scheduleReconnect() {
    this._retry++;
    const delay = Math.min(1000 * Math.pow(2, this._retry), 15000);
    setTimeout(() => {
      if (API.access && !this._closedByUser) this.open();
    }, delay);
  },

  send(obj) {
    if (this.conn && this.conn.readyState === WebSocket.OPEN) {
      this.conn.send(JSON.stringify(obj));
      return true;
    }
    return false;
  },

  join(chatID) {
    if (this.joinedChats.has(chatID)) return;
    this.joinedChats.add(chatID);
    this.send({ type: 'chat.join', chat_id: chatID });
  },

  leave(chatID) {
    if (!this.joinedChats.has(chatID)) return;
    this.joinedChats.delete(chatID);
    this.send({ type: 'chat.leave', chat_id: chatID });
  },

  sendText(chatID, content) {
    return this.send({ type: 'message.send', chat_id: chatID, content });
  },

  close() {
    this._closedByUser = true;
    this.joinedChats.clear();
    clearInterval(this._pingTimer);
    this._pingTimer = null;
    if (this.conn) this.conn.close();
    this.connected = false;
  },
};
