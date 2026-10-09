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
  await loadChats();
  WS.open();
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

function renderChatList() {
  const list = $('chat-list');
  if (!chats.length) {
    list.innerHTML = '<div class="chat-list-empty">Чатов пока нет.<br>Нажмите «Добавить +».</div>';
    return;
  }
  list.innerHTML = chats.map(c => {
    const active = currentChat && currentChat.id === c.id ? ' active' : '';
    const name = chatDisplayName(c);
    const preview = c.last_message_at
      ? fmtTime(c.last_message_at)
      : 'Нет сообщений';
    return `<div class="chat-item${active}" onclick="openChat('${c.id}')">
      <div class="chat-avatar">${esc(initials(name))}</div>
      <div class="chat-info">
        <div class="chat-name">${esc(name)}</div>
        <div class="chat-preview">${esc(preview)}</div>
      </div>
    </div>`;
  }).join('');
}

async function openChat(chatID) {
  currentChat = chats.find(c => c.id === chatID) || null;
  if (!currentChat) return;
  renderChatList();
  $('chat-title').textContent = chatDisplayName(currentChat);
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
  return `<div class="message ${isMine ? 'outgoing' : 'incoming'}">
    <div class="message-avatar">${esc(initials(isMine ? (currentUser && currentUser.username) : who))}</div>
    <div class="message-body">
      ${!isMine && who ? '<div class="message-sender">' + esc(who) + '</div>' : ''}
      <div class="message-bubble">${esc(m.content)}</div>
      <div class="message-time">${fmtTime(m.created_at)}</div>
    </div>
  </div>`;
}

function renderMessages(msgs) {
  const box = $('chat-messages');
  if (!msgs.length) {
    box.innerHTML = '<div class="messages-hint">Сообщений пока нет — напишите первое!</div>';
    return;
  }
  box.innerHTML = msgs.map(messageHTML).join('');
  box.scrollTop = box.scrollHeight;
}

function appendMessage(m) {
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
function onWSMessage(ev) {
  if (ev.type !== 'message.new') return;
  const mine = ev.sender && currentUser && ev.sender.id === currentUser.id;

  // Основной случай: собеседник сейчас открыл этот чат — добавляем в реальном времени.
  if (currentChat && ev.chat_id === currentChat.id) {
    appendMessage(ev);
  } else if (mine) {
    // Собственное сообщение из другого окна/вкладки, где этот чат открыт:
    // не показываем дубль, только обновим превью списка.
    const c = chats.find(x => x.id === ev.chat_id);
    if (c) c.last_message_at = ev.created_at;
    renderChatList();
    return;
  } else {
    // Чат открыт в другой вкладке этого браузера — синхронизируем ленту между вкладками.
    broadcastToOtherTabs({ kind: 'message.new', chat_id: ev.chat_id, msg: ev });
  }

  // Обновляем превью времени и порядок чатов в списке.
  const c = chats.find(x => x.id === ev.chat_id);
  if (c) {
    c.last_message_at = ev.created_at;
    chats.sort((a, b) => new Date(b.last_message_at || 0) - new Date(a.last_message_at || 0));
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
      const c = chats.find(x => x.id === p.chat_id);
      if (c) {
        c.last_message_at = p.msg.created_at;
        renderChatList();
      }
      // Если этот чат открыт в текущей вкладке — добавляем сообщение в ленту.
      if (currentChat && currentChat.id === p.chat_id) appendMessage(p.msg);
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
