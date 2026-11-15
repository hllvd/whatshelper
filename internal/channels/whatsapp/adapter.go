package whatsapp

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/store/sqlstore"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	waLog "go.mau.fi/whatsmeow/util/log"
	"google.golang.org/protobuf/proto"
	_ "modernc.org/sqlite"

	"whatshelper/internal/channels/bridge"
	"whatshelper/internal/core"
)

// Config configura o adaptador WhatsApp baseado em whatsmeow + SQLite puro em Go.
type Config struct {
	DBPath         string
	TenantID       string
	Account        string
	AdminPhoneE164 string
	AllowGroups    bool
	PairPhone      string
	BridgeClient   *bridge.Client
	Logger         *slog.Logger
}

// QRStatus representa o estado atual de pareamento para exposição segura no endpoint local.
type QRStatus struct {
	Paired    bool      `json:"paired"`
	Connected bool      `json:"connected"`
	Code      string    `json:"code,omitempty"`
	Event     string    `json:"event,omitempty"`
	Detail    string    `json:"detail,omitempty"`
	UpdatedAt time.Time `json:"updated_at"`
}

// Adapter gerencia a sessão whatsmeow e implementa bridge.OutboundHandler.
type Adapter struct {
	cfg    Config
	logger *slog.Logger

	db     *sql.DB
	client *whatsmeow.Client

	mu          sync.RWMutex
	connected   bool
	connectedAt time.Time
	paired      bool
	qrStatus    QRStatus
	stopCtx   context.Context
	stopFunc  context.CancelFunc
}

// BuildSQLiteDSN constrói a string de conexão SQLite compatível com modernc.org/sqlite e sqlstore.Upgrade.
func BuildSQLiteDSN(dbPath string) string {
	clean := strings.TrimSpace(dbPath)
	if clean == "" {
		clean = ":memory:"
	}
	if strings.HasPrefix(clean, "file:") {
		if strings.Contains(clean, "?") {
			return clean + "&_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)"
		}
		return clean + "?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)"
	}
	if clean == ":memory:" {
		return "file::memory:?cache=shared&_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)"
	}
	return fmt.Sprintf("file:%s?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)", clean)
}

// OpenSQLiteContainer inicializa e migra o banco SQLite puro em Go utilizado pelo whatsmeow.
func OpenSQLiteContainer(ctx context.Context, dbPath string, waLogger waLog.Logger) (*sqlstore.Container, *sql.DB, error) {
	trimmed := strings.TrimSpace(dbPath)
	if trimmed != "" && trimmed != ":memory:" {
		if err := os.MkdirAll(filepath.Dir(trimmed), 0o755); err != nil {
			return nil, nil, fmt.Errorf("whatsapp: falha ao criar diretório SQLite: %w", err)
		}
	}
	db, err := sql.Open("sqlite", BuildSQLiteDSN(trimmed))
	if err != nil {
		return nil, nil, fmt.Errorf("whatsapp: falha ao abrir SQLite: %w", err)
	}
	db.SetMaxOpenConns(1)

	container := sqlstore.NewWithDB(db, "sqlite3", waLogger)
	if err := container.Upgrade(ctx); err != nil {
		_ = db.Close()
		return nil, nil, fmt.Errorf("whatsapp: falha no upgrade do schema sqlstore: %w", err)
	}
	return container, db, nil
}

// New cria um Adapter pronto para iniciar conexão e pareamento.
func New(cfg Config) (*Adapter, error) {
	if strings.TrimSpace(cfg.TenantID) == "" {
		cfg.TenantID = "demo"
	}
	if strings.TrimSpace(cfg.Account) == "" {
		cfg.Account = "main"
	}
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return &Adapter{
		cfg:    cfg,
		logger: logger,
		qrStatus: QRStatus{
			Detail:    "inicializando",
			UpdatedAt: time.Now().UTC(),
		},
	}, nil
}

// Start inicializa o banco SQLite, carrega ou cria dispositivo whatsmeow e inicia conexão.
func (a *Adapter) Start(ctx context.Context) error {
	a.stopCtx, a.stopFunc = context.WithCancel(ctx)

	container, db, err := OpenSQLiteContainer(ctx, a.cfg.DBPath, waLog.Noop)
	if err != nil {
		return err
	}
	a.db = db

	deviceStore, err := container.GetFirstDevice(ctx)
	if err != nil {
		_ = db.Close()
		return fmt.Errorf("whatsapp: falha ao carregar deviceStore: %w", err)
	}

	cli := whatsmeow.NewClient(deviceStore, waLog.Noop)
	cli.EnableAutoReconnect = true
	cli.InitialAutoReconnect = true
	cli.AutoTrustIdentity = true

	cli.AddEventHandler(a.handleEvent)
	a.client = cli

	if cli.Store.ID == nil {
		a.setStatus(false, false, "", "qr_pending", "aguardando pareamento via QR code")
		qrChan, err := cli.GetQRChannel(a.stopCtx)
		if err != nil {
			return fmt.Errorf("whatsapp: erro ao iniciar canal de QR: %w", err)
		}
		go a.consumeQRChannel(qrChan)
	} else {
		a.setStatus(true, false, "", "connecting", "sessão carregada, conectando")
	}

	go a.connectWithRetry(cli)
	return nil
}

func (a *Adapter) connectWithRetry(cli *whatsmeow.Client) {
	delay := time.Second
	for {
		if a.stopCtx.Err() != nil {
			return
		}
		err := cli.Connect()
		if err == nil {
			return
		}
		a.logger.Warn("falha temporária ao conectar ao WhatsApp; nova tentativa agendada",
			slog.Duration("retry_in", delay),
			slog.Any("error", err),
		)
		paired := cli.Store.ID != nil
		a.setStatus(paired, false, "", "connect_retry", "aguardando conectividade WhatsApp")
		timer := time.NewTimer(delay)
		select {
		case <-a.stopCtx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
		if delay < 30*time.Second {
			delay *= 2
		}
	}
}

// Stop desconecta o cliente WhatsApp e fecha o banco SQLite com segurança.
func (a *Adapter) Stop(ctx context.Context) error {
	if a.stopFunc != nil {
		a.stopFunc()
	}
	if a.client != nil {
		a.client.Disconnect()
	}
	if a.db != nil {
		return a.db.Close()
	}
	return nil
}

func (a *Adapter) Health() core.ChannelHealth {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return core.ChannelHealth{
		Healthy: a.connected && a.paired,
		Detail:  a.qrStatus.Detail,
	}
}

func (a *Adapter) QRState() QRStatus {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.qrStatus
}

// Send envia resposta de texto ao número E.164 correspondente via whatsmeow.
func (a *Adapter) Send(ctx context.Context, msg core.NormalizedMessage) error {
	if a.client == nil {
		return errors.New("whatsapp: cliente não inicializado")
	}
	digits, err := NormalizeE164(msg.SourceKey)
	if err != nil {
		return err
	}
	jid := types.NewJID(digits, types.DefaultUserServer)
	waMsg := &waE2E.Message{
		Conversation: proto.String(msg.Text),
	}
	if id := strings.TrimSpace(msg.MessageID); id != "" {
		_, err = a.client.SendMessage(ctx, jid, waMsg, whatsmeow.SendRequestExtra{
			ID: types.MessageID(id),
		})
		return err
	}
	_, err = a.client.SendMessage(ctx, jid, waMsg)
	return err
}

// SendTyping atualiza o indicador de digitação no chat do contato.
func (a *Adapter) SendTyping(ctx context.Context, sourceKey string, typing bool) error {
	if a.client == nil || !a.Health().Healthy {
		return nil
	}
	digits, err := NormalizeE164(sourceKey)
	if err != nil {
		return err
	}
	jid := types.NewJID(digits, types.DefaultUserServer)
	presence := types.ChatPresencePaused
	if typing {
		presence = types.ChatPresenceComposing
	}
	return a.client.SendChatPresence(ctx, jid, presence, types.ChatPresenceMediaText)
}

func (a *Adapter) setStatus(paired, connected bool, code, ev, detail string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.paired = paired
	a.connected = connected
	if code != "" || ev == "success" {
		a.qrStatus.Code = code
	}
	a.qrStatus.Paired = paired
	a.qrStatus.Connected = connected
	a.qrStatus.Event = ev
	a.qrStatus.Detail = detail
	a.qrStatus.UpdatedAt = time.Now().UTC()
}

func (a *Adapter) consumeQRChannel(qrChan <-chan whatsmeow.QRChannelItem) {
	pairTriggered := false
	for evt := range qrChan {
		switch evt.Event {
		case "code":
			a.setStatus(false, false, evt.Code, "code", "QR code pronto para leitura no /qr")
			a.logger.Info("QR Code WhatsApp gerado — abra GET /qr no connector ou escaneie",
				slog.String("qr_code", evt.Code),
			)
			if !pairTriggered && strings.TrimSpace(a.cfg.PairPhone) != "" && a.client != nil {
				pairTriggered = true
				if phone, err := NormalizeE164(a.cfg.PairPhone); err == nil {
					if code, err := a.client.PairPhone(a.stopCtx, phone, true, whatsmeow.PairClientChrome, "Chrome (Linux)"); err == nil {
						a.logger.Info("Pair code WhatsApp gerado", slog.String("pair_code", code))
					} else {
						a.logger.Warn("falha ao gerar pair code WhatsApp", slog.Any("error", err))
					}
				}
			}
		case "success":
			a.setStatus(true, true, "", "success", "pareado e conectado")
			a.logger.Info("WhatsApp pareado com sucesso")
		default:
			a.setStatus(false, false, "", evt.Event, "pareamento encerrado: "+evt.Event)
			a.logger.Warn("evento do canal QR WhatsApp", slog.String("event", evt.Event))
		}
	}
}

func (a *Adapter) handleEvent(rawEvt any) {
	switch v := rawEvt.(type) {
	case *events.Connected:
		a.mu.Lock()
		a.connectedAt = time.Now().UTC()
		a.mu.Unlock()
		a.setStatus(true, true, "", "connected", "conectado ao WhatsApp")
		a.logger.Info("sessão WhatsApp conectada")
	case *events.Disconnected:
		paired := a.client != nil && a.client.Store.ID != nil
		a.setStatus(paired, false, "", "disconnected", "desconectado; reconexão automática ativa")
		a.logger.Warn("sessão WhatsApp desconectada")
	case *events.LoggedOut:
		a.setStatus(false, false, "", "logged_out", "sessão deslogada: "+v.PermanentDisconnectDescription())
		a.logger.Error("sessão WhatsApp deslogada; requer novo pareamento")
	case *events.PairSuccess:
		a.setStatus(true, false, "", "pair_success", "pareamento concluído, conectando")
	case *events.Message:
		a.handleInboundMessage(v)
	case *events.ChatPresence:
		a.handleChatPresence(v)
	}
}

func (a *Adapter) handleChatPresence(evt *events.ChatPresence) {
	if evt == nil || a.cfg.BridgeClient == nil {
		return
	}
	if !ShouldProcessInbound(evt.IsFromMe, evt.IsGroup, false, a.cfg.AllowGroups) {
		return
	}
	sourceKey, err := ResolveSenderPhone(evt.MessageSource)
	if err != nil || sourceKey == "" {
		return
	}
	typing := evt.State == types.ChatPresenceComposing
	ctx, cancel := context.WithTimeout(a.stopCtx, 5*time.Second)
	defer cancel()
	if err := a.cfg.BridgeClient.SendTypingEvent(ctx, bridge.TypingRequest{
		TenantID:  a.cfg.TenantID,
		Channel:   "whatsapp",
		SourceKey: sourceKey,
		Typing:    typing,
	}); err != nil {
		a.logger.Debug("falha ao encaminhar typing do WhatsApp", slog.Any("error", err))
	}
}

func (a *Adapter) handleInboundMessage(evt *events.Message) {
	if evt == nil || a.cfg.BridgeClient == nil {
		return
	}
	if !ShouldProcessInbound(evt.Info.IsFromMe, evt.Info.IsGroup, evt.Info.IsIncomingBroadcast(), a.cfg.AllowGroups) {
		return
	}
	a.mu.RLock()
	connectedAt := a.connectedAt
	a.mu.RUnlock()
	if ShouldIgnoreHistoryFromMe(evt.Info.IsFromMe, evt.Info.Timestamp, connectedAt) {
		return
	}
	parsed, ok := ExtractContent(evt.Message)
	if !ok {
		return
	}

	ctx, cancel := context.WithTimeout(a.stopCtx, 20*time.Second)
	defer cancel()

	var mediaRef string
	if parsed.Downloadable != nil {
		data, err := a.client.Download(ctx, parsed.Downloadable)
		if err != nil {
			a.logger.Warn("falha ao baixar mídia do WhatsApp", slog.Any("error", err))
		} else {
			sourceKey, _ := ResolveSenderPhone(evt.Info.MessageSource)
			uploadedRef, err := a.cfg.BridgeClient.UploadMediaWithRetry(ctx, bridge.MediaUploadParams{
				TenantID:  a.cfg.TenantID,
				Channel:   "whatsapp",
				SourceKey: sourceKey,
				MediaID:   evt.Info.ID,
				Ext:       parsed.Ext,
				MimeType:  parsed.MimeType,
				Timestamp: evt.Info.Timestamp,
			}, data)
			if err != nil {
				a.logger.Warn("falha ao enviar mídia para o Brain", slog.Any("error", err))
			} else {
				mediaRef = uploadedRef
			}
		}
	}

	req, err := BuildInboundRequest(a.cfg.TenantID, a.cfg.Account, a.cfg.AdminPhoneE164, evt, parsed, mediaRef)
	if err != nil {
		a.logger.Warn("falha ao normalizar remetente WhatsApp", slog.Any("error", err))
		return
	}

	if _, err := a.cfg.BridgeClient.SendInboundWithRetry(ctx, req); err != nil {
		a.logger.Error("falha ao entregar inbound do WhatsApp ao Brain",
			slog.String("channel", "whatsapp"),
			slog.String("source_key", req.SourceKey),
			slog.String("message_id", req.MessageID),
			slog.Any("error", err),
		)
	}
}
