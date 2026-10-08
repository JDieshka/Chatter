// WebSocket-клиент: ws://{origin}/ws?token={access}
// Протокол сервера:
//   клиент -> сервер: {type:"chat.join"|"chat.leave"|"message.send", chat_id, content}
//   сервер -> клиент: {type:"message.new", id, chat_id, sender:{...}, content, created_at}
//                      {type:"error", code, message}
const WS = {
  conn: null,
  connected: false,
  joinedChat: null,   // текущий чат, на который подписаны
  onMessage: null,    // callback(event)
  onStatus: null,     // callback(connected:boolean)
  onError: null,      // callback({code,message})
  _retry: 0,
  _closedByUser: false,

  open() {
    if (!API.access) return;
    this._closedByUser = false;
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
      // переподписка на текущий чат после реконнекта
      if (this.joinedChat) this.send({ type: 'chat.join', chat_id: this.joinedChat });
    };

    this.conn.onmessage = (ev) => {
      let data;
      try { data = JSON.parse(ev.data); } catch (e) { return; }
      if (data.type === 'error') {
        console.warn('WS error:', data.code, data.message);
        if (this.onError) this.onError(data);
        return;
      }
      if (this.onMessage) this.onMessage(data);
    };

    this.conn.onclose = () => {
      this.connected = false;
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
    if (this.joinedChat === chatID) return;
    this.leave();
    this.joinedChat = chatID;
    this.send({ type: 'chat.join', chat_id: chatID });
  },

  leave() {
    if (this.joinedChat) this.send({ type: 'chat.leave', chat_id: this.joinedChat });
    this.joinedChat = null;
  },

  sendText(chatID, content) {
    return this.send({ type: 'message.send', chat_id: chatID, content });
  },

  close() {
    this._closedByUser = true;
    this.joinedChat = null;
    if (this.conn) this.conn.close();
    this.connected = false;
  },
};
