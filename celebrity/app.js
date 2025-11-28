// partybox.js
(function() {
  const statusEl = document.getElementById('status');
  const gameInfoEl = document.getElementById('game-info');
  const celebsEl = document.getElementById('celebs');
  const userNameEl = document.getElementById('user-name');

  const modPanel = document.getElementById('mod-panel');
  const lockBtn = document.getElementById('lock-btn');
  const startBtn = document.getElementById('start-btn');
  const lockStatusEl = document.getElementById('lock-status');
  const playersBody = document.getElementById('players-body');

  const guessModal = document.getElementById('guess-modal');
  const guessTextEl = document.getElementById('guess-text');
  const guessTargetSelect = document.getElementById('guess-target');
  const guessCancelBtn = document.getElementById('guess-cancel');
  const guessConfirmBtn = document.getElementById('guess-confirm');

  const qrBtn = document.getElementById('qr-btn');
  const qrModal = document.getElementById('qr-modal');
  const qrImage = document.getElementById('qr-image');
  const qrClose = document.getElementById('qr-close');

  let username = '';
  let celeb = '';
  let isModerator = false;
  let lobbyLocked = false;
  let wasKicked = false;
  let gameStarted = false;
  let currentTurnUser = '';
  let amOut = false;
  let activePlayers = [];  // usernames of active players (not out)
  let eliminatedList = []; // usernames of eliminated players
  let pendingCelebrity = '';

  let ws = null;
  let connectAttempts = 0;
  const MAX_CONNECT_ATTEMPTS = 8;
  const CONNECT_TIMEOUT_MS = 4000;
  let connectWatchdog = null;

  function wsURL() {
    const proto = (location.protocol === 'https:') ? 'wss://' : 'ws://';
    const wsPath = location.pathname.replace(/\/$/, '') + '/ws';
    return proto + location.host + wsPath;
  }

  function clearWatchdog() {
    if (connectWatchdog !== null) {
      clearTimeout(connectWatchdog);
      connectWatchdog = null;
    }
  }

  function safeSend(obj) {
    if (!ws || ws.readyState !== WebSocket.OPEN) {
      console.warn('WS not open; dropping message', obj);
      return;
    }
    ws.send(JSON.stringify(obj));
  }

  function connectWebSocket() {
    if (ws && (ws.readyState === WebSocket.OPEN || ws.readyState === WebSocket.CONNECTING)) {
      return;
    }

    connectAttempts++;
    statusEl.textContent = 'Connecting…';

    ws = new WebSocket(wsURL());

    // Watchdog: if we never get onopen within N ms, force close so onclose can retry.
    clearWatchdog();
    connectWatchdog = setTimeout(function() {
      if (!ws) return;
      if (ws.readyState === WebSocket.CONNECTING) {
        console.warn('WS still CONNECTING, forcing close to trigger retry');
        try { ws.close(); } catch (e) {}
      }
    }, CONNECT_TIMEOUT_MS);

    ws.onopen = function() {
      clearWatchdog();
      connectAttempts = 0;
      statusEl.textContent = 'Connected.';
    };

    ws.onmessage = function(event) {
      try {
        const msg = JSON.parse(event.data);

        if (msg.type === 'session_info') {
          handleSessionInfo(msg);
          return;
        }

        if (msg.type === 'celebrity_list' && Array.isArray(msg.celebrities)) {
          renderCelebs(msg.celebrities);
          return;
        }

        if (msg.type === 'collision') {
          handleCollision(msg);
          return;
        }

        if (msg.type === 'lobby_state') {
          lobbyLocked = !!msg.locked;
          if (isModerator) {
            updateLockUI();
          }
          statusEl.textContent = lobbyLocked
            ? 'Lobby is locked. No new players may join.'
            : 'Lobby is unlocked.';
          return;
        }

        if (msg.type === 'lobby_locked') {
          statusEl.textContent = msg.message;
          return;
        }

        if (msg.type === 'kicked') {
          wasKicked = true;
          const text = msg.message || 'You have been kicked.';
          statusEl.textContent = text;
          try { ws.close(); } catch (e) {}
          return;
        }

        if (msg.type === 'moderator_view') {
          isModerator = true;
          modPanel.style.display = 'block';

          lobbyLocked = !!msg.lobby_locked;
          updateLockUI();
          if (Array.isArray(msg.players)) {
            renderModeratorPlayers(msg.players);
          }
          return;
        }

        if (msg.type === 'game_state') {
          updateGameInfo(msg);
          return;
        }

        if (msg.type === 'guess_result') {
          statusEl.textContent = msg.message || '';
          return;
        }

        if (msg.type === 'not_your_turn' || msg.type === 'guess_error') {
          statusEl.textContent = msg.message || '';
          return;
        }
      } catch (e) {
        console.error('bad message', e);
      }
    };

    ws.onclose = function() {
      clearWatchdog();
      if (wasKicked) {
        return;
      }
      if (connectAttempts >= MAX_CONNECT_ATTEMPTS) {
        statusEl.textContent = 'Disconnected. Unable to reconnect.';
        return;
      }
      statusEl.textContent = 'Disconnected. Reconnecting…';
      setTimeout(connectWebSocket, Math.min(1000 * connectAttempts, 5000));
    };

    ws.onerror = function() {
      // Let onclose handle the retry logic; just surface a message if not kicked.
      if (!wasKicked) {
        statusEl.textContent = 'WebSocket error. Retrying…';
      }
    };
  }

  function sendJoin() {
    if (!username || !celeb) return;
    safeSend({
      type: 'join',
      username: username,
      celebrity: celeb
    });
  }

  function updateLockUI() {
    lockBtn.textContent = lobbyLocked ? 'Unlock lobby' : 'Lock lobby';
    lockStatusEl.textContent = lobbyLocked
      ? 'Lobby is locked; no new players may join.'
      : 'Lobby is unlocked; new players may join.';
  }

  function renderModeratorPlayers(players) {
    playersBody.innerHTML = '';
    players.forEach(function(p) {
      const tr = document.createElement('tr');

      const tdUser = document.createElement('td');
      tdUser.textContent = p.username;

      const tdCeleb = document.createElement('td');
      tdCeleb.textContent = p.celebrity;

      const tdActions = document.createElement('td');
      const btn = document.createElement('button');
      btn.type = 'button';
      btn.className = 'kick-btn';
      btn.dataset.username = p.username;
      btn.textContent = 'Kick';
      tdActions.appendChild(btn);

      tr.appendChild(tdUser);
      tr.appendChild(tdCeleb);
      tr.appendChild(tdActions);

      playersBody.appendChild(tr);
    });
  }

  function describeTeams(teams) {
    if (!Array.isArray(teams) || !teams.length) return '';
    const parts = teams.map(function(t) {
      const leader = t.leader || '(unknown)';
      const members = Array.isArray(t.members) && t.members.length
        ? ' [' + t.members.join(', ') + ']'
        : '';
      return leader + members;
    });
    return parts.join(' | ');
  }

  function updateGameInfo(state) {
    gameStarted = !!state.started;
    currentTurnUser = state.current_turn || '';
    eliminatedList = Array.isArray(state.eliminated) ? state.eliminated.slice() : [];
    activePlayers = [];

    if (Array.isArray(state.turn_order)) {
      state.turn_order.forEach(function(name) {
        if (eliminatedList.indexOf(name) === -1) {
          activePlayers.push(name);
        }
      });
    }

    amOut = username && eliminatedList.indexOf(username) !== -1;
    const teamsText = describeTeams(state.teams || []);

    gameInfoEl.classList.remove('your-turn');

    const lines = [];

    if (!gameStarted) {
      if (state.winner) {
        lines.push('Winner: ' + state.winner);
      }
      if (teamsText) {
        lines.push('Teams: ' + teamsText);
      }
      gameInfoEl.innerHTML = lines.map(function(t) {
        return '<div>' + t + '</div>';
      }).join('');
      if (isModerator) {
        startBtn.disabled = false;
      }
      return;
    }

    if (isModerator) {
      startBtn.disabled = true;
    }

    if (username && !amOut && currentTurnUser === username) {
      lines.push('Current turn: YOU!');
      gameInfoEl.classList.add('your-turn');
    } else if (currentTurnUser) {
      lines.push('Current turn: ' + currentTurnUser);
    } else {
      lines.push('Current turn: —');
    }

    if (eliminatedList.length) {
      lines.push('Out: ' + eliminatedList.join(', '));
    }
    if (teamsText) {
      lines.push('Teams: ' + teamsText);
    }

    gameInfoEl.innerHTML = lines.map(function(t) {
      return '<div>' + t + '</div>';
    }).join('');
  }

  function openGuessModal(celebrity) {
    if (!gameStarted) return;
    if (!username || isModerator || amOut) return;
    if (currentTurnUser && currentTurnUser !== username) {
      statusEl.textContent = 'It is ' + currentTurnUser + '\'s turn.';
      return;
    }

    const options = activePlayers.filter(function(name) {
      return name && name !== username;
    });

    if (!options.length) {
      statusEl.textContent = 'No other players to guess.';
      return;
    }

    pendingCelebrity = celebrity;
    guessTextEl.textContent = 'Whose celebrity is "' + celebrity + '"?';
    guessTargetSelect.innerHTML = '';
    options.forEach(function(name) {
      const opt = document.createElement('option');
      opt.value = name;
      opt.textContent = name;
      guessTargetSelect.appendChild(opt);
    });

    guessModal.style.display = 'flex';
  }

  function closeGuessModal() {
    pendingCelebrity = '';
    guessModal.style.display = 'none';
  }

  guessCancelBtn.addEventListener('click', function() {
    closeGuessModal();
  });

  guessConfirmBtn.addEventListener('click', function() {
    if (!pendingCelebrity) {
      closeGuessModal();
      return;
    }
    const target = guessTargetSelect.value;
    if (!target) return;
    safeSend({
      type: 'guess',
      celebrity: pendingCelebrity,
      target_username: target
    });
    closeGuessModal();
  });

  // QR modal wiring
  qrBtn.addEventListener('click', function() {
    const base = location.pathname.replace(/\/$/, '');
    qrImage.src = base + '/qr';
    qrModal.style.display = 'flex';
  });

  qrClose.addEventListener('click', function() {
    qrModal.style.display = 'none';
  });

  qrModal.addEventListener('click', function(e) {
    if (e.target === qrModal) {
      qrModal.style.display = 'none';
    }
  });

  function handleSessionInfo(msg) {
    lobbyLocked = !!msg.lobby_locked;
    const isExisting = !!msg.is_existing;
    isModerator = !!msg.is_moderator;
    const existingName = msg.username || '';

    if (lobbyLocked && !isExisting && !isModerator) {
      statusEl.textContent = 'Lobby is locked; no new players may join.';
      return;
    }

    if (isModerator) {
      if (existingName) {
        userNameEl.textContent = existingName;
      } else {
        userNameEl.textContent = 'Moderator';
      }
      statusEl.textContent = lobbyLocked
        ? 'You are the moderator. Lobby is locked.'
        : 'You are the moderator. Lobby is unlocked.';
      modPanel.style.display = 'block';
      updateLockUI();
      return;
    }

    if (isExisting) {
      if (existingName) {
        username = existingName;
        userNameEl.textContent = existingName;
      }
      statusEl.textContent = lobbyLocked
        ? 'Rejoined as existing player; lobby is locked.'
        : 'Rejoined as existing player.';
      return;
    }

    // New, non-moderator player, lobby is open → prompt username + celebrity
    statusEl.textContent = 'Lobby is unlocked. Please join the game.';
    username = prompt('Enter your username:') || '';
    if (!username) return;
    userNameEl.textContent = username;
    celeb = prompt('Enter a celebrity name:') || '';
    if (!celeb) return;
    sendJoin();
  }

  function renderCelebs(list) {
    celebsEl.innerHTML = '';
    list.forEach(function(c) {
      const li = document.createElement('li');
      const span = document.createElement('span');
      span.textContent = c;
      li.appendChild(span);
      li.addEventListener('click', function() {
        openGuessModal(c);
      });
      celebsEl.appendChild(li);
    });
  }

  function handleCollision(msg) {
    statusEl.textContent = msg.message;

    if (msg.field === 'username') {
      const newUsername = prompt(msg.message, username) || '';
      if (!newUsername) return;
      username = newUsername;
      userNameEl.textContent = username;
    } else if (msg.field === 'celebrity') {
      const newCeleb = prompt(msg.message, celeb) || '';
      if (!newCeleb) return;
      celeb = newCeleb;
    }

    sendJoin();
  }

  // Moderator: lock/unlock lobby
  lockBtn.addEventListener('click', function() {
    if (!isModerator) return;
    const newLock = !lobbyLocked;
    safeSend({
      type: 'lock_lobby',
      lock: newLock
    });
  });

  // Moderator: start game
  startBtn.addEventListener('click', function() {
    if (!isModerator) return;
    if (gameStarted) return;
    safeSend({
      type: 'start_game'
    });
  });

  // Moderator: kick players (event delegation on table body)
  playersBody.addEventListener('click', function(e) {
    if (!isModerator) return;
    const btn = e.target.closest('button.kick-btn');
    if (!btn) return;
    const targetUsername = btn.dataset.username;
    if (!targetUsername) return;

    if (!confirm('Kick ' + targetUsername + '?')) {
      return;
    }

    safeSend({
      type: 'kick',
      target_username: targetUsername
    });
  });

  // Kick off initial connection
  connectWebSocket();
})();
