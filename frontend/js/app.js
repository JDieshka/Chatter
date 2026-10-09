// app.js — связывает UI с REST API и WebSocket.

let currentUser = null;   // {id, username, email, ...}
let chats = [];           // [{id, type, title, members:[{user_id,username,role}], last_message_at}]
let currentChat = null;   // выбранный чат (объект из chats)
let oldestMsgId = 0;      // курсор пагинации: id самого раннего загруженного сообщения
let hasMore = false;      // есть ли ещё история старше курсора

const PAGE_SIZE = 50;

const $ = (id) => document.getElementById(id);

// ---------- Инициализация / загрузка профиля ----------
async function boot() {
  WS.onMessage = onWSMessage;
  WS.onStatus = (ok) => setWsStatus(ok);
  WS.onError = (e) => {
    if (e.code === 'CHAT_NOT_FOUND' || e.code === 'FORBIDDEN') loadChats();
    console.warn('WS:', e.code, e.message);
  };

  if (!API.access && !API.refresh) {
    showPage('auth');
    return;
  }
  try {
    currentUser = await API.me();
  } catch (e) {
    API.clearTokens();
    showPage('auth');
    return;
  }
  renderUserHeader();
  // Сначала открываем WS: тогда loadChats() успеет подписаться на все чаты,
  // и новые сообщения прилетают в реальном времени даже для закрытых чатов.
  WS.open();
  await loadChats();
  showPage('app');
}

function renderUserHeader() {
  if (!currentUser) return;
  const nick = '#' + currentUser.username;
  const hdr = $('hdr-nickname');
  if (hdr) hdr.textContent = nick;
  const hdrAv = $('hdr-avatar');
  if (hdrAv) hdrAv.textContent = initials(currentUser.username);

  const pName = $('profile-name');
  if (pName) pName.textContent = currentUser.username;
  const pNick = $('profile-nick');
  if (pNick) pNick.textContent = nick;
  const pAv = $('profile-avatar');
  if (pAv) pAv.textContent = initials(currentUser.username);
  const pUser = $('profile-username');
  if (pUser) pUser.value = currentUser.username;
  const pEmail = $('profile-email');
  if (pEmail) pEmail.value = currentUser.email || '';
}

// ---------- Auth ----------
async function doLogin() {
  setError('login-error', '');
  const username = $('login-username').value.trim();
  const password = $('login-password').value;
  if (!username || !password) {
    setError('login-error', 'Заполните никнейм и пароль');
    return;
  }
  try {
    currentUser = await API.login(username, password);
    renderUserHeader();
    await loadChats();
    WS.open();
    showPage('app');
  } catch (e) {
    setError('login-error', e.message);
  }
}

async function doRegister() {
  setError('register-error', '');
  const username = $('reg-username').value.trim();
  const email = $('reg-email').value.trim();
  const password = $('reg-password').value;
  const password2 = $('reg-password2').value;
  if (!username || !password) {
    setError('register-error', 'Заполните никнейм и пароль');
    return;
  }
  if (password !== password2) {
    setError('register-error', 'Пароли не совпадают');
    return;
  }
  try {
    currentUser = await API.register(username, email, password);
    renderUserHeader();
    await loadChats();
    WS.open();
    showPage('app');
  } catch (e) {
    setError('register-error', e.message);
  }
}

async function doLogout() {
  WS.close();
  await API.logout();
  currentUser = null;
  currentChat = null;
  chats = [];
  renderChatList();
  renderMessages([]);
  showPage('auth');
}

function setError(id, msg) {
  const el = $(id);
  if (el) el.textContent = msg;
}

// ---------- Чаты ----------
async function loadChats() {
  try {
    chats = await API.listChats();
  } catch (e) {
    console.error('listChats:', e);
    chats = [];
  }
  // Серверные данные точнее локальных — сбрасываем кэш счётчиков/превью.
  Object.keys(unread).forEach(k => delete unread[k]);
  Object.keys(lastPreview).forEach(k => delete lastPreview[k]);
  // Открытый чат не может быть непрочитанным.
  if (currentChat) currentChat.unread_count = 0;
  // Подписка на каждый чат (сервер проверяет членство): новые сообщения в любом
  // чате прилетают в реальном времени, даже если этот чат сейчас не открыт.
  chats.forEach(c => WS.join(c.id));
  renderChatList();
}

// Отображаемое имя чата: для private — ник собеседника, для group — title.
function chatDisplayName(chat) {
  if (chat.type === 'private') {
    const peer = (chat.members || []).find(m => m.user_id !== (currentUser && currentUser.id));
    return peer ? peer.username : 'Личный чат';
  }
  return chat.title || 'Группа';
}

// ID собеседника в приватном чате (для открытия профиля по клику).
function chatPeerID(chat) {
  if (!chat || chat.type !== 'private') return '';
  const peer = (chat.members || []).find(m => m.user_id !== (currentUser && currentUser.id));
  return peer ? peer.user_id : '';
}

function renderChatList() {
  const list = $('chat-list');
  if (!chats.length) {
    list.innerHTML = '<div class="chat-list-empty">Чатов пока нет.<br>Нажмите «Добавить +».</div>';
    return;
  }
  list.innerHTML = chats.map(c => {
    const active = currentChat && currentChat.id === c.id ? ' active' : '';
    const name = chatDisplayName(c);
    // Превью: текст последнего сообщения (сохранённый локально или с сервера),
    // если сообщений нет — время создания чата.
    const prev = lastPreview[c.id];
    const content = (prev ? prev.content : (c.last_content || '')).trim();
    const when = fmtTime((prev && prev.created_at) || c.last_message_at || c.created_at);
    const previewText = content
      ? (content.length > 40 ? content.slice(0, 40) + '…' : content)
      : 'Нет сообщений';
    const unreadCount = Math.max(unread[c.id] || 0, c.unread_count || 0);
    const badge = unreadCount > 0
      ? `<div class="unread-badge">${unreadCount > 99 ? '99+' : unreadCount}</div>`
      : '';
    const peerId = chatPeerID(c);
    const avAttr = peerId ? ` onclick="event.stopPropagation(); openUserProfile('${peerId}')" title="Профиль пользователя"` : '';
    return `<div class="chat-item${active}" onclick="openChat('${c.id}')">
      <div class="chat-avatar"${avAttr}>${esc(initials(name))}${badge}</div>
      <div class="chat-info">
        <div class="chat-name">${esc(name)}</div>
        <div class="chat-preview" title="${esc(content)}">${esc(previewText)}</div>
        <div class="chat-time">${esc(when)}</div>
      </div>
    </div>`;
  }).join('');
}

async function openChat(chatID) {
  currentChat = chats.find(c => c.id === chatID) || null;
  if (!currentChat) return;
  clearUnread(chatID);
  if (currentChat) currentChat.unread_count = 0;
  renderChatList();
  $('chat-title').textContent = chatDisplayName(currentChat);
  const infoBtn = $('chat-info-btn');
  if (infoBtn) infoBtn.style.display = '';
  $('msg-input').disabled = false;
  $('msg-input').focus();

  oldestMsgId = 0;
  hasMore = false;
  try {
    const page = await API.getMessages(chatID, 0, PAGE_SIZE);
    const msgs = page.messages || [];
    hasMore = !!page.has_more;
    if (msgs.length) oldestMsgId = page.next_before || msgs[0].id;
    renderMessages(msgs);
  } catch (e) {
    $('chat-messages').innerHTML = '<div class="messages-hint">Не удалось загрузить историю: ' + esc(e.message) + '</div>';
  }
  WS.join(chatID);
}


// ---------- Информация о чате ----------
async function openChatInfo() {
  if (!currentChat) return;
  $('chat-info-error').textContent = '';
  $('chat-info-title').textContent = chatDisplayName(currentChat);
  $('chat-info-body').innerHTML = '<div class="messages-hint">Загрузка…</div>';
  openModal('chat-info');
  try {
    const info = await API.getChatInfo(currentChat.id);
    $('chat-info-body').innerHTML = chatInfoHTML(info);
  } catch (e) {
    $('chat-info-error').textContent = 'Не удалось загрузить информацию: ' + e.message;
    $('chat-info-body').innerHTML = '';
  }
}

function chatInfoHTML(info) {
  const typeLabel = info.type === 'group' ? 'Групповой чат' : 'Приватный чат';
  const creatorName = info.creator ? info.creator.username : '—';
  let html = `<div class="chat-info-meta">
      Тип: <b>${esc(typeLabel)}</b><br>`;
  if (info.title) html += `Название: <b>${esc(info.title)}</b><br>`;
  html += `Создатель: <b>${esc(creatorName)}</b><br>
      Создан: <b>${fmtDate(info.created_at)}</b><br>
      Участников: <b>${info.member_count}</b>
    </div>
    <div class="chat-info-section-title">Участники</div>`;
  html += (info.members || []).map(m => {
    let tag = '';
    if (m.is_creator) tag = '<span class="member-tag creator">создатель</span>';
    else if (m.role === 'admin') tag = '<span class="member-tag admin">админ</span>';
    const me = currentUser && m.user_id === currentUser.id ? ' (вы)' : '';
    return `<div class="chat-member-row" onclick="openUserProfile('${m.user_id}')" title="Профиль пользователя">
      <div class="chat-member-avatar">${esc(initials(m.username))}</div>
      <div class="chat-member-name">#${esc(m.username)}${esc(me)}</div>
      <div class="chat-member-joined">${fmtDate(m.joined_at)}</div>
      ${tag}
    </div>`;
  }).join('');
  return html;
}

// ---------- Профиль пользователя (свой или чужой) ----------
async function openUserProfile(userID) {
  if (!userID) return;
  // Свой ID — открываем обычный профиль с редактированием.
  if (currentUser && userID === currentUser.id) { openModal('profile'); return; }

  $('user-profile-error').textContent = '';
  $('user-profile-name').textContent = '—';
  $('user-profile-nick').textContent = '—';
  $('user-profile-avatar').textContent = '?';
  $('user-profile-since').textContent = 'Загрузка…';
  const startBtn = $('user-profile-start-chat');
  startBtn.style.display = 'none';
  startBtn.dataset.chatId = '';
  startBtn.dataset.peer = '';
  openModal('user-profile');

  try {
    const p = await API.getUser(userID);
    $('user-profile-name').textContent = p.username;
    $('user-profile-nick').textContent = '#' + p.username;
    $('user-profile-avatar').textContent = initials(p.username);
    $('user-profile-since').textContent = 'На платформе с ' + fmtDate(p.created_at);
    if (p.chat_id) {
      startBtn.textContent = 'Открыть чат';
      startBtn.dataset.chatId = p.chat_id;
      startBtn.style.display = '';
    } else {
      startBtn.textContent = 'Написать сообщение';
      startBtn.dataset.peer = p.username;
      startBtn.style.display = '';
    }
  } catch (e) {
    $('user-profile-error').textContent = 'Не удалось загрузить профиль: ' + e.message;
  }
}

// Из модалки профиля: открыть существующий приватный чат или создать новый.
async function userProfileStartChat() {
  const btn = $('user-profile-start-chat');
  const existingID = btn.dataset.chatId;
  const peerName = btn.dataset.peer;
  closeModal('user-profile');
  if (existingID) {
    // если чат уже в списке — просто открываем, иначе обновляем список
    if (chats.some(c => c.id === existingID)) { openChat(existingID); return; }
    await loadChats();
    if (chats.some(c => c.id === existingID)) openChat(existingID);
    return;
  }
  if (!peerName) return;
  try {
    const res = await API.createPrivateChat(peerName);
    const chat = res.chat || res; // сервер может вернуть {chat:...} или сам чат
    if (res.created !== false && !chats.some(c => c.id === chat.id)) {
      chats.unshift(Object.assign({ unread_count: 0 }, chat));
    }
    await loadChats(); // актуальные поля (members, last_message) с сервера
    const found = chats.find(c => c.id === chat.id);
    if (found) openChat(found.id);
  } catch (e) {
    setError('user-profile-error', e.message);
    openModal('user-profile');
  }
}

// Подгрузка более ранней истории по курсору before (id самого раннего загруженного сообщения)
async function loadOlderMessages() {
  if (!currentChat || !hasMore || !oldestMsgId) return;
  try {
    const page = await API.getMessages(currentChat.id, oldestMsgId, PAGE_SIZE);
    const msgs = page.messages || [];
    hasMore = !!page.has_more;
    if (msgs.length) oldestMsgId = page.next_before || msgs[0].id;
    prependMessages(msgs);
  } catch (e) {
    console.error('loadOlderMessages:', e);
  }
}

function prependMessages(msgs) {
  const box = $('chat-messages');
  const prevHeight = box.scrollHeight;
  const hint = box.querySelector('.messages-hint');
  if (hint) hint.remove();
  box.insertAdjacentHTML('afterbegin', msgs.map(messageHTML).join(''));
  box.scrollTop = box.scrollHeight - prevHeight; // сохраняем позицию просмотра
}

function messageHTML(m) {
  const outgoing = m.sender && currentUser && m.sender.id === currentUser.id;
  // у сообщений из REST нет объекта sender — сверяем по sender_id
  const isMine = outgoing || m.sender_id === (currentUser && currentUser.id);
  const who = m.sender ? m.sender.username : '';
  const senderId = (m.sender && m.sender.id) || m.sender_id || '';
  const mid = m.id ? ` id="msg-${m.id}"` : '';
  const avAttr = (!isMine && senderId)
    ? ` onclick="openUserProfile('${senderId}')" title="Профиль пользователя" style="cursor:pointer;"`
    : '';
  const nameAttr = (!isMine && senderId)
    ? ` onclick="openUserProfile('${senderId}')" style="cursor:pointer;"`
    : '';
  return `<div class="message ${isMine ? 'outgoing' : 'incoming'}"${mid}>
    <div class="message-avatar"${avAttr}>${esc(initials(isMine ? (currentUser && currentUser.username) : who))}</div>
    <div class="message-body">
      ${!isMine && who ? '<div class="message-sender"' + nameAttr + '>' + esc(who) + '</div>' : ''}
      <div class="message-bubble">${esc(m.content)}</div>
      <div class="message-time">${fmtTime(m.created_at)}</div>
    </div>
  </div>`;
}

function renderMessages(msgs) {
  seenMsgIDs.clear();
  msgs.forEach(m => { if (m.id) seenMsgIDs.add(m.id); });
  const box = $('chat-messages');
  if (!msgs.length) {
    box.innerHTML = '<div class="messages-hint">Сообщений пока нет — напишите первое!</div>';
    return;
  }
  box.innerHTML = msgs.map(messageHTML).join('');
  box.scrollTop = box.scrollHeight;
}

function appendMessage(m) {
  if (m.id && document.getElementById('msg-' + m.id)) return; // уже показан
  const box = $('chat-messages');
  const hint = box.querySelector('.messages-hint');
  if (hint) hint.remove();
  box.insertAdjacentHTML('beforeend', messageHTML(m));
  box.scrollTop = box.scrollHeight;
}

// ---------- Отправка сообщений ----------
async function sendCurrentMessage() {
  const input = $('msg-input');
  const text = input.value.trim();
  if (!text || !currentChat) return;
  input.value = '';
  // основной путь — WebSocket; fallback — REST
  if (!WS.sendText(currentChat.id, text)) {
    try {
      const m = await API.sendMessage(currentChat.id, text);
      if (m && m.id) seenMsgIDs.add(m.id);
      appendMessage(m);
    } catch (e) {
      input.value = text;
      alert('Не удалось отправить сообщение: ' + e.message);
    }
  }
}

// Enter в поле ввода — отправка
document.addEventListener('keydown', (e) => {
  if (e.key === 'Enter' && e.target && e.target.id === 'msg-input') {
    e.preventDefault();
    sendCurrentMessage();
  }
});

// Подгрузка истории при прокрутке к самому верху
document.addEventListener('scroll', (e) => {
  if (e.target && e.target.id === 'chat-messages' && e.target.scrollTop < 40) {
    loadOlderMessages();
  }
}, true);

// ---------- Приём событий WebSocket ----------
const seenMsgIDs = new Set(); // защита от дублей (пересылка между вкладками, реконнект)

// Локальные счётчики непрочитанных и превью: сервер присылает их в GET /api/chats,
// а между перезагрузками обновляем на лету из WS-событий.
const unread = {};        // chat_id -> число непрочитанных пришедших сообщений
const lastPreview = {};   // chat_id -> { content, created_at } последнего сообщения

function rememberPreview(chatID, msg) {
  if (!chatID || !msg) return;
  lastPreview[chatID] = { content: msg.content || '', created_at: msg.created_at };
}

function bumpUnread(chatID) {
  if (!chatID) return;
  unread[chatID] = (unread[chatID] || 0) + 1;
}

function clearUnread(chatID) {
  delete unread[chatID];
}

function onWSMessage(ev) {
  // Новый чат создан собеседником (или приглашением в группу) — добавляем
  // его в список без перезагрузки страницы.
  if (ev.type === 'chat.created') {
    const chat = ev.chat;
    if (chat && !chats.find(c => c.id === chat.id)) {
      chats.unshift(chat);
      WS.join(chat.id);
      renderChatList();
      broadcastToOtherTabs({ kind: 'chats:updated' });
    }
    return;
  }

  if (ev.type !== 'message.new') return;
  if (ev.id && seenMsgIDs.has(ev.id)) return;
  if (ev.id) seenMsgIDs.add(ev.id);
  const mine = (ev.sender_id && currentUser && ev.sender_id === currentUser.id) ||
               (ev.sender && currentUser && ev.sender.id === currentUser.id);

  rememberPreview(ev.chat_id, ev);

  // Основной случай: собеседник сейчас открыл этот чат — добавляем в реальном времени.
  if (currentChat && ev.chat_id === currentChat.id) {
    appendMessage(ev);
    if (!mine) clearUnread(ev.chat_id); // читаем прямо сейчас
  } else if (mine) {
    // Собственное сообщение из другого окна/вкладки, где этот чат открыт:
    // не показываем дубль и не считаем его непрочитанным.
  } else {
    // Чат закрыт: увеличиваем счётчик непрочитанных и уведомляем другие вкладки.
    bumpUnread(ev.chat_id);
    broadcastToOtherTabs({ kind: 'message.new', chat_id: ev.chat_id, msg: ev });
  }

  // Обновляем превью времени и порядок чатов в списке.
  const c = chats.find(x => x.id === ev.chat_id);
  if (c) {
    c.last_message_at = ev.created_at;
    if (ev.content != null) c.last_content = ev.content;
    chats.sort((a, b) => new Date(b.last_message_at || 0) - new Date(a.last_message_at || 0));
    renderChatList();
  } else {
    renderChatList();
  }
}

// Синхронизация состояний между вкладками одного браузера (BroadcastChannel).
const tabSync = ('BroadcastChannel' in window) ? new BroadcastChannel('chatter-tab-sync') : null;

function broadcastToOtherTabs(payload) {
  if (tabSync) { try { tabSync.postMessage(payload); } catch (e) {} }
}

if (tabSync) {
  tabSync.onmessage = (e) => {
    const p = e.data;
    if (!p) return;
    if (p.kind === 'message.new') {
      if (p.msg && p.msg.id && seenMsgIDs.has(p.msg.id)) return;
      if (p.msg && p.msg.id) seenMsgIDs.add(p.msg.id);
      rememberPreview(p.chat_id, p.msg);
      const mineTab = (p.msg.sender_id && currentUser && p.msg.sender_id === currentUser.id) ||
                      (p.msg.sender && currentUser && p.msg.sender.id === currentUser.id);
      const c = chats.find(x => x.id === p.chat_id);
      if (c) {
        c.last_message_at = p.msg.created_at;
        if (p.msg.content != null) c.last_content = p.msg.content;
      }
      if (currentChat && currentChat.id === p.chat_id) {
        appendMessage(p.msg);
        if (!mineTab) clearUnread(p.chat_id);
      } else if (!mineTab) {
        bumpUnread(p.chat_id);
      }
      renderChatList();
    } else if (p.kind === 'chats:updated') {
      loadChats();
    }
  };
}

function setWsStatus(connected) {
  let el = $('ws-status');
  if (!el) {
    el = document.createElement('div');
    el.id = 'ws-status';
    el.className = 'ws-status';
    const area = document.querySelector('#page-app .app-container');
    if (area && area.firstChild) area.insertBefore(el, area.children[1]);
  }
  el.textContent = connected ? '' : 'Соединение потеряно, переподключение…';
  el.classList.toggle('off', !connected);
}

// ---------- Создание чата (модалка) ----------
function onChatTypeChange() {
  const t = $('new-chat-type').value;
  $('private-fields').style.display = t === 'private' ? 'flex' : 'none';
  $('group-fields').style.display = t === 'group' ? 'flex' : 'none';
}

async function createChat() {
  setError('modal-error', '');
  const type = $('new-chat-type').value;
  try {
    let chat;
    if (type === 'private') {
      const peer = $('new-peer').value.trim();
      if (!peer) throw new Error('Укажите никнейм пользователя');
      chat = await API.createPrivateChat(peer);
    } else {
      const title = $('new-group-title').value.trim();
      if (!title) throw new Error('Укажите название группы');
      const members = $('new-group-members').value
        .split(',').map(s => s.trim()).filter(Boolean);
      chat = await API.createGroupChat(title, members);
    }
    closeModal('add-channel');
    $('new-peer').value = '';
    $('new-group-title').value = '';
    $('new-group-members').value = '';
    await loadChats();
    // открываем созданный чат
    const found = chats.find(c => c.id === chat.id) || chat;
    if (!chats.find(c => c.id === chat.id)) { chats.unshift(found); renderChatList(); }
    openChat(chat.id);
    broadcastToOtherTabs({ kind: 'chats:updated' });
  } catch (e) {
    setError('modal-error', e.message);
  }
}

// Старт
window.addEventListener('DOMContentLoaded', boot);
