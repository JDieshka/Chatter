// voice.js — голосовые подключения (WebRTC, mesh P2P).
// Сигналинг идёт через существующий WebSocket сервера:
//   клиент -> сервер: {type:"voice.join"|"voice.leave", chat_id}
//                     {type:"voice.offer"|"voice.answer"|"voice.ice", chat_id, target_user_id, payload}
//   сервер -> клиент: {type:"voice.peer.joined"|"voice.peer.left", chat_id, user_id, username}
//                     {type:"voice.offer"|"voice.answer"|"voice.ice", chat_id, user_id, username, target, payload}
// Медиа-трафик между клиентами идёт напрямую (STUN), сервер его не трогает.

const ICE_SERVERS = [{ urls: 'stun:stun.l.google.com:19302' }];

const Voice = {
  roomId: null,          // id комнаты, к которой подключён голос сейчас
  localStream: null,
  peers: new Map(),      // peerUserID -> { pc, offerMade }
  pendingCandidates: {}, // peerUserID -> [candidates до установления connection]
  peerMuted: {},         // peerUserID -> true, если у него выключен микрофон
  speaking: {},          // peerUserID -> говорит ли сейчас
  micMuted: false,
  _audioCtxs: {},

  // --- интеграция с UI ---

  // Вызывается из app.js при открытии чата: показать/скрыть панель подключения.
  onChatSwitch(chatID) {
    const bar = document.getElementById('voice-connect-bar');
    if (!bar) return;
    // Определяем тип комнаты по текущему открытому чату (global currentChat из app.js):
    // локальная переменная `let chats` не доступна как window.chats, поэтому
    // обращаемся к currentChat напрямую.
    const chat = (typeof currentChat !== 'undefined' && currentChat && currentChat.id === chatID)
      ? currentChat
      : (typeof chats !== 'undefined' ? (chats.find(c => c.id === chatID)) : null);
    const isVoice = !!(chat && chat.type === 'voice');
    bar.style.display = isVoice ? 'flex' : 'none';
    this._renderBar();
  },

  _renderBar() {
    const btn = document.getElementById('voice-connect-btn');
    const status = document.getElementById('voice-connect-status');
    const muteBtn = document.getElementById('voice-mute-btn');
    if (!btn) return;
    if (this.roomId) {
      btn.textContent = '📴 Выйти из голоса';
      btn.classList.add('connected');
      if (status) status.textContent = 'В сети: ' + (this.peers.size + 1) + ' участник(ов)';
      if (muteBtn) {
        muteBtn.disabled = false;
        muteBtn.textContent = this.micMuted ? '🔇' : '🎤';
        muteBtn.title = this.micMuted ? 'Включить микрофон' : 'Выключить микрофон';
        muteBtn.onclick = () => this.toggleMute();
      }
    } else {
      btn.textContent = '🎤 Подключиться к голосу';
      btn.classList.remove('connected');
      if (status) status.textContent = '';
      if (muteBtn) { muteBtn.disabled = true; muteBtn.textContent = '🎤'; muteBtn.onclick = null; }
    }
  },

  toggleVoiceConnect() {
    // currentChat — переменная модуля app.js (let), она НЕ доступна как window.currentChat.
    const chat = (typeof currentChat !== 'undefined' && currentChat) || window.currentChat;
    if (this.roomId) this.leave();
    else if (chat && chat.type === 'voice') this.join(chat.id);
  },

  // --- подключение ---

  async join(roomId) {
    if (this.roomId) this.leave();
    const bar = document.getElementById('voice-connect-status');
    if (bar) bar.textContent = 'Запрашиваем доступ к микрофону…';
    let stream;
    try {
      stream = await navigator.mediaDevices.getUserMedia({
        audio: { echoCancellation: true, noiseSuppression: true, autoGainControl: true },
      });
    } catch (e) {
      if (bar) bar.textContent = '';
      // getUserMedia доступен только в secure context (HTTPS или localhost).
      // На HTTP с IP-адреса VPS браузер молча блокирует микрофон — предупредим явно.
      if (!window.isSecureContext || e.name === 'NotAllowedError' || e.name === 'NotAllowedError') {
        alert('Браузер запретил доступ к микрофону.\n\n' +
          'Самая частая причина: страница открыта по http:// (не защищённое соединение).\n' +
          'Доступ к микрофону работает только по HTTPS (или на localhost).\n' +
          'Подключите сертификат (Caddy/Nginx + Let\'s Encrypt) и откройте сайт по https://.\n\n' +
          'Если сайт уже по HTTPS — разрешите микрофон в настройках прав сайта браузера.');
      } else {
        alert('Не удалось получить доступ к микрофону: ' + e.message);
      }
      return;
    }
    this.localStream = stream;
    this.roomId = roomId;
    this.micMuted = false;
    WS.send({ type: 'voice.join', chat_id: roomId });
    this._renderBar();
    if (window.renderVoiceParticipants) window.renderVoiceParticipants();
  },

  leave() {
    if (!this.roomId) return;
    const roomId = this.roomId;
    WS.send({ type: 'voice.leave', chat_id: roomId });
    this._teardown();
    this._renderBar();
    if (window.renderVoiceParticipants) window.renderVoiceParticipants();
  },

  _teardown() {
    this.peers.forEach((p) => { try { p.pc.close(); } catch (e) {} });
    this.peers.clear();
    this.pendingCandidates = {};
    this.peerMuted = {};
    this.speaking = {};
    Object.keys(this._audioCtxs).forEach(k => { try { this._audioCtxs[k].close(); } catch (e) {} });
    this._audioCtxs = {};
    if (this.localStream) {
      this.localStream.getTracks().forEach(t => t.stop());
      this.localStream = null;
    }
    this.roomId = null;
    this.micMuted = false;
  },

  toggleMute() {
    if (!this.localStream) return;
    this.micMuted = !this.micMuted;
    this.localStream.getAudioTracks().forEach(t => { t.enabled = !this.micMuted; });
    this._renderBar();
    if (window.renderVoiceParticipants) window.renderVoiceParticipants();
  },

  // --- обработка входящих событий хаба (вызывает app.js) ---

  onSignal(ev) {
    if (!ev.chat_id || ev.chat_id !== this.roomId) {
      // presence-события полезны даже без собственного подключения — обновим список,
      // но сигналинг чужих комнат игнорируем полностью
      return;
    }
    switch (ev.type) {
      case 'voice.peer.joined':
        // Новый участник в комнате: ждём его offer (он инициатор по протоколу).
        this._ensurePeer(ev.user_id);
        break;
      case 'voice.peer.left':
        this._removePeer(ev.user_id);
        break;
      case 'voice.offer':
        if (ev.target && ev.target !== (window.currentUser && window.currentUser.id)) return;
        this._handleOffer(ev);
        break;
      case 'voice.answer':
        if (ev.target && ev.target !== (window.currentUser && window.currentUser.id)) return;
        this._handleAnswer(ev);
        break;
      case 'voice.ice':
        if (ev.target && ev.target !== (window.currentUser && window.currentUser.id)) return;
        this._handleIce(ev);
        break;
    }
    this._renderBar();
    if (window.renderVoiceParticipants) window.renderVoiceParticipants();
  },

  _sendTo(target, type, payload) {
    WS.send({ type, chat_id: this.roomId, target_user_id: target, payload });
  },

  _ensurePeer(peerID) {
    if (peerID === (window.currentUser && window.currentUser.id)) return null;
    let p = this.peers.get(peerID);
    if (p) return p;
    const pc = new RTCPeerConnection({ iceServers: ICE_SERVERS });
    p = { pc, offerMade: false };
    this.peers.set(peerID, p);

    if (this.localStream) {
      this.localStream.getTracks().forEach(track => pc.addTrack(track, this.localStream));
    }

    pc.onicecandidate = (e) => {
      if (e.candidate) this._sendTo(peerID, 'voice.ice', e.candidate.toJSON ? e.candidate.toJSON() : e.candidate);
    };
    pc.ontrack = (e) => {
      const stream = e.streams[0];
      if (!stream) return;
      this._attachRemoteAudio(peerID, stream);
      this._detectSpeaking(peerID, stream);
    };
    pc.onconnectionstatechange = () => {
      if (pc.connectionState === 'failed' || pc.connectionState === 'closed') this._removePeer(peerID);
      this._renderBar();
    };
    return p;
  },

  async _handleOffer(ev) {
    const from = ev.user_id;
    const p = this._ensurePeer(from);
    if (!p) return;
    try {
      await p.pc.setRemoteDescription(new RTCSessionDescription(ev.payload));
      await this._drainCandidates(from);
      const answer = await p.pc.createAnswer();
      await p.pc.setLocalDescription(answer);
      this._sendTo(from, 'voice.answer', p.pc.localDescription);
      // Мы ответили — отправляем свой offer тоже, чтобы установить двусторонний канал
      // (offerer мог создать коннект до добавления нашего локального трека).
      if (!p.offerMade) {
        p.offerMade = true;
        const offer = await p.pc.createOffer();
        await p.pc.setLocalDescription(offer);
        this._sendTo(from, 'voice.offer', p.pc.localDescription);
      }
    } catch (e) {
      console.error('voice offer handling:', e);
    }
    this._renderBar();
  },

  async _handleAnswer(ev) {
    const from = ev.user_id;
    const p = this.peers.get(from);
    if (!p) return;
    try {
      await p.pc.setRemoteDescription(new RTCSessionDescription(ev.payload));
      await this._drainCandidates(from);
    } catch (e) {
      console.error('voice answer handling:', e);
    }
  },

  async _handleIce(ev) {
    const from = ev.user_id;
    const p = this.peers.get(from);
    if (!p) {
      if (!this.pendingCandidates[from]) this.pendingCandidates[from] = [];
      this.pendingCandidates[from].push(ev.payload);
      return;
    }
    if (!p.pc.remoteDescription) {
      if (!this.pendingCandidates[from]) this.pendingCandidates[from] = [];
      this.pendingCandidates[from].push(ev.payload);
      return;
    }
    try { await p.pc.addIceCandidate(new RTCIceCandidate(ev.payload)); } catch (e) { /* ignore */ }
  },

  async _drainCandidates(peerID) {
    const list = this.pendingCandidates[peerID] || [];
    delete this.pendingCandidates[peerID];
    const p = this.peers.get(peerID);
    if (!p) return;
    for (const cand of list) {
      try { await p.pc.addIceCandidate(new RTCIceCandidate(cand)); } catch (e) { /* ignore */ }
    }
  },

  _removePeer(peerID) {
    const p = this.peers.get(peerID);
    if (p) { try { p.pc.close(); } catch (e) {} this.peers.delete(peerID); }
    delete this.pendingCandidates[peerID];
    delete this.peerMuted[peerID];
    delete this.speaking[peerID];
    if (this._audioCtxs[peerID]) { try { this._audioCtxs[peerID].close(); } catch (e) {} delete this._audioCtxs[peerID]; }
    this._renderBar();
    if (window.renderVoiceParticipants) window.renderVoiceParticipants();
  },

  // Удалённый аудио-поток прячем в <audio>, иначе WebRTC его не воспроизведёт.
  _attachRemoteAudio(peerID, stream) {
    if (document.getElementById('remote-audio-' + peerID)) return;
    const a = document.createElement('audio');
    a.id = 'remote-audio-' + peerID;
    a.autoplay = true;
    a.srcObject = stream;
    document.body.appendChild(a);
    a.play().catch(() => {});
  },

  // Индикатор «говорит»: уровень сигнала через WebAudio AnalyserNode.
  _detectSpeaking(peerID, stream) {
    if (this._audioCtxs[peerID]) return;
    try {
      const Ctx = window.AudioContext || window.webkitAudioContext;
      const ctx = new Ctx();
      this._audioCtxs[peerID] = ctx;
      const src = ctx.createMediaStreamSource(stream);
      const analyser = ctx.createAnalyser();
      analyser.fftSize = 512;
      src.connect(analyser);
      const data = new Uint8Array(analyser.frequencyBinCount);
      const tick = () => {
        if (!this._audioCtxs[peerID]) return;
        analyser.getByteFrequencyData(data);
        let sum = 0;
        for (let i = 0; i < data.length; i++) sum += data[i];
        const wasSpeaking = !!this.speaking[peerID];
        this.speaking[peerID] = sum / data.length > 8;
        if (wasSpeaking !== this.speaking[peerID]) {
          // лёгкое обновление карточки без полной перерисовки
          const card = document.querySelector('.participant-card[data-uid="' + peerID + '"]');
          if (card) card.classList.toggle('speaking', this.speaking[peerID]);
        }
        setTimeout(tick, 300);
      };
      tick();
    } catch (e) { /* индикатор не критичен */ }
  },
};

// Глобальные обработчики для inline-onclick в index.html
window.toggleVoiceConnect = () => Voice.toggleVoiceConnect();
