// Чистый UI: навигация, табы, модалки — без обращений к API.

function showPage(pageId) {
  document.querySelectorAll('.page-view').forEach(p => p.classList.remove('active'));
  const page = document.getElementById('page-' + pageId);
  if (page) {
    page.classList.add('active');
    page.style.display = 'flex';
  }
}

function switchAuthTab(tab) {
  const loginTab = document.getElementById('tab-login');
  const registerTab = document.getElementById('tab-register');
  const loginForm = document.getElementById('form-login');
  const registerForm = document.getElementById('form-register');

  if (tab === 'login') {
    loginTab.classList.add('active-login');
    registerTab.classList.remove('active-register');
    loginForm.classList.remove('hidden');
    registerForm.classList.add('hidden');
  } else {
    registerTab.classList.add('active-register');
    loginTab.classList.remove('active-login');
    registerForm.classList.remove('hidden');
    loginForm.classList.add('hidden');
  }
}

function switchAppTab(tab) {
  const chatsView = document.getElementById('view-chats');
  const voiceView = document.getElementById('view-voice');
  const chatsBtn = document.getElementById('app-tab-chats');
  const voiceBtn = document.getElementById('app-tab-voice');

  if (tab === 'chats') {
    chatsView.style.display = 'flex';
    voiceView.style.display = 'none';
    chatsBtn.classList.add('active-cyan');
    voiceBtn.classList.remove('active-purple');
  } else {
    voiceView.style.display = 'flex';
    chatsView.style.display = 'none';
    voiceBtn.classList.add('active-purple');
    chatsBtn.classList.remove('active-cyan');
  }
}

function openModal(id) {
  document.getElementById('modal-' + id).classList.add('show');
}

function closeModal(id) {
  document.getElementById('modal-' + id).classList.remove('show');
  const err = document.getElementById('modal-error');
  if (err) err.textContent = '';
}

document.querySelectorAll('.modal-overlay').forEach(overlay => {
  overlay.addEventListener('click', function (e) {
    if (e.target === this) this.classList.remove('show');
  });
});

// Утилиты отображения
function esc(s) {
  const d = document.createElement('div');
  d.textContent = s == null ? '' : String(s);
  return d.innerHTML;
}

function initials(name) {
  if (!name) return '?';
  return name.trim().charAt(0).toUpperCase();
}

function fmtTime(iso) {
  try {
    const d = new Date(iso);
    const now = new Date();
    const hm = d.toLocaleTimeString('ru-RU', { hour: '2-digit', minute: '2-digit' });
    if (d.toDateString() === now.toDateString()) return hm;
    return d.toLocaleDateString('ru-RU', { day: '2-digit', month: '2-digit' }) + ' ' + hm;
  } catch (e) { return ''; }
}
