// REST API client. Все запросы идут на тот же origin, с которого загружен фронтенд.
const API = {
  access: localStorage.getItem('access_token') || '',
  refresh: localStorage.getItem('refresh_token') || '',

  setTokens(data) {
    if (data.access_token) {
      this.access = data.access_token;
      localStorage.setItem('access_token', this.access);
    }
    if (data.refresh_token) {
      this.refresh = data.refresh_token;
      localStorage.setItem('refresh_token', this.refresh);
    }
  },

  clearTokens() {
    this.access = '';
    this.refresh = '';
    localStorage.removeItem('access_token');
    localStorage.removeItem('refresh_token');
  },

  async raw(method, path, body) {
    const opts = { method, headers: {} };
    if (body !== undefined) {
      opts.headers['Content-Type'] = 'application/json';
      opts.body = JSON.stringify(body);
    }
    if (this.access) opts.headers['Authorization'] = 'Bearer ' + this.access;
    const res = await fetch(path, opts);
    let data = null;
    try { data = await res.json(); } catch (e) { /* пустое тело */ }
    return { status: res.status, data };
  },

  // Запрос с автоматическим refresh при 401
  async call(method, path, body) {
    let r = await this.raw(method, path, body);
    if (r.status === 401 && this.refresh) {
      const rr = await this.raw('POST', '/api/auth/refresh', { refresh_token: this.refresh });
      if (rr.status === 200) {
        this.setTokens(rr.data);
        r = await this.raw(method, path, body);
      } else {
        this.clearTokens();
      }
    }
    if (r.status >= 400) {
      throw new Error((r.data && r.data.error) || ('HTTP ' + r.status));
    }
    return r.data;
  },

  // ---- Auth ----
  async register(username, email, password) {
    const d = await this.call('POST', '/api/auth/register', { username, email, password });
    this.setTokens(d);
    return d.user;
  },

  async login(username, password) {
    const d = await this.call('POST', '/api/auth/login', { username, password });
    this.setTokens(d);
    return d.user;
  },

  async logout() {
    // Сервер не имеет эндпоинта выхода — просто очищаем токены на клиенте.
    this.clearTokens();
  },

  // ---- Users ----
  me()            { return this.call('GET', '/api/users/me'); },
  getUser(id)     { return this.call('GET', '/api/users/' + encodeURIComponent(id)); },

  // ---- Chats ----
  listChats()     { return this.call('GET', '/api/chats'); },
  getChat(id)     { return this.call('GET', '/api/chats/' + id); },
  getChatInfo(id) { return this.call('GET', '/api/chats/' + id + '/info'); },
  createPrivateChat(peerUsername) {
    return this.call('POST', '/api/chats/private', { peer_username: peerUsername });
  },
  createGroupChat(title, memberUsernames) {
    return this.call('POST', '/api/chats/group', { title, member_usernames: memberUsernames });
  },
  createVoiceRoom(title) {
    // Участники не выбираются: в голосовую комнату может зайти любой пользователь.
    return this.call('POST', '/api/chats/voice', { title });
  },
  addMember(chatId, username) {
    return this.call('POST', '/api/chats/' + chatId + '/members', { username });
  },

  // ---- Messages ----
  // before — id самого раннего загруженного сообщения (курсор назад по истории).
  // Ответ сервера: { messages: [...], next_before: <id>, has_more: bool }
  getMessages(chatId, before, limit) {
    let q = '/api/chats/' + chatId + '/messages?limit=' + (limit || 50);
    if (before) q += '&before=' + before;
    return this.call('GET', q);
  },
  sendMessage(chatId, content) {
    return this.call('POST', '/api/chats/' + chatId + '/messages', { content });
  },
};
