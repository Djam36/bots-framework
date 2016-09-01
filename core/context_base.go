package bots

import (
	"fmt"
	"golang.org/x/net/context"
	"google.golang.org/appengine"
	"google.golang.org/appengine/datastore"
	"net/http"
	"github.com/strongo/app"
	"github.com/strongo/measurement-protocol"
	"strings"
	"strconv"
)

type WebhookContextBase struct {
	//w          http.ResponseWriter
	r             *http.Request
	logger        strongo.Logger
	botAppContext BotAppContext
	BotContext    BotContext
	botPlatform   BotPlatform
	WebhookInput

	locale        strongo.Locale

	//update      tgbotapi.Update
	chatEntity    BotChat

	BotUserKey    *datastore.Key
	appUser       BotAppUser
	strongo.Translator
	//Locales    strongo.LocalesProvider

	BotCoreStores

	gaMeasurement *measurement.BufferedSender
}

func(whcb *WebhookContextBase) ExecutionContext() strongo.ExecutionContext {
	return whcb
}

func(whcb *WebhookContextBase) BotAppContext() BotAppContext {
	return whcb.botAppContext
}

func NewWebhookContextBase(r *http.Request, botAppContext BotAppContext, botPlatform BotPlatform, botContext BotContext, webhookInput WebhookInput, botCoreStores BotCoreStores, gaMeasurement *measurement.BufferedSender) *WebhookContextBase {
	whcb := WebhookContextBase{
		r:             r,
		gaMeasurement: gaMeasurement,
		logger: botContext.BotHost.Logger(r),
		botAppContext: botAppContext,
		botPlatform:   botPlatform,
		BotContext:    botContext,
		WebhookInput:  webhookInput,
		BotCoreStores: botCoreStores,
	}
	whcb.Translator = botAppContext.GetTranslator(whcb.Logger())
	return &whcb
}

func(whcb *WebhookContextBase) GaMeasurement() *measurement.BufferedSender {
	return whcb.gaMeasurement
}

func(whcb *WebhookContextBase) GaCommon() measurement.Common {
	if whcb.chatEntity != nil {
		c := whcb.Context()
		return measurement.Common{
			UserID: strconv.FormatInt(whcb.chatEntity.GetAppUserIntID(), 10),
			UserLanguage: strings.ToLower(whcb.chatEntity.GetPreferredLanguage()),
			ClientID: whcb.chatEntity.GetGaClientID().String(),
			ApplicationID: fmt.Sprintf("bot.%v.%v", whcb.botPlatform.Id(), whcb.GetBotCode()),
			UserAgent: fmt.Sprintf("%v bot (%v:%v) %v", whcb.botPlatform.Id(), appengine.AppID(c), appengine.VersionID(c), whcb.r.Host),
			DataSource: "bot",
		}
	}
	return measurement.Common{
		DataSource: "bot",
		ClientID: "c7ea15eb-3333-4d47-a002-9d1a14996371",
	}
}

func (whcb *WebhookContextBase) BotPlatform() BotPlatform {
	return whcb.botPlatform
}

func (whcb *WebhookContextBase) Logger() strongo.Logger {
	return whcb.logger
}

func (whcb *WebhookContextBase) GetBotSettings() BotSettings {
	return whcb.BotContext.BotSettings
}

func (whcb *WebhookContextBase) GetBotCode() string {
	return whcb.BotContext.BotSettings.Code
}

func (whcb *WebhookContextBase) GetBotToken() string {
	return whcb.BotContext.BotSettings.Token
}

func (whcb *WebhookContextBase) Translate(key string, args ...interface{}) string {
	s := whcb.Translator.Translate(key, whcb.Locale().Code5)
	if len(args) > 0 {
		s = fmt.Sprintf(s, args...)
	}
	return s
}

func (whcb *WebhookContextBase) TranslateNoWarning(key string, args ...interface{}) string {
	s := whcb.Translator.TranslateNoWarning(key, whcb.locale.Code5)
	if len(args) > 0 {
		s = fmt.Sprintf(s, args...)
	}
	return s
}

func (whcb *WebhookContextBase) GetHttpClient() *http.Client {
	return whcb.BotContext.BotHost.GetHttpClient(whcb.r)
}

func (whcb *WebhookContextBase) HasChatEntity() bool {
	return whcb.chatEntity != nil
}

func (whcb *WebhookContextBase) SaveAppUser(appUserID int64, appUserEntity BotAppUser) error {
	return whcb.BotAppUserStore.SaveAppUser(appUserID, appUserEntity)
}

func (whcb *WebhookContextBase) SetChatEntity(chatEntity BotChat) {
	whcb.chatEntity = chatEntity
}

func (whcb *WebhookContextBase) ChatEntity(whc WebhookContext) (BotChat, error) {
	if whcb.chatEntity == nil {
		err := whcb.getChatEntityBase(whc)
		if err != nil {
			return nil, err
		}
	}
	return whcb.chatEntity, nil
}

func (whcb *WebhookContextBase) GetOrCreateBotUserEntityBase() (BotUser, error) {
	logger := whcb.Logger()
	logger.Debugf("GetOrCreateBotUserEntityBase()")
	botUserID := whcb.GetSender().GetID()
	botUser, err := whcb.GetBotUserById(botUserID)
	if err != nil {
		return nil, err
	}
	if botUser == nil {
		logger.Infof("Bot user entity not found, creating a new one...")
		botUser, err = whcb.CreateBotUser(whcb.GetSender())
		if err != nil {
			logger.Errorf("Failed to create bot user: %v", err)
			return nil, err
		}
		logger.Infof("Bot user entity created")

		if whcb.GetBotSettings().Mode == Production {
			gaEvent := measurement.NewEvent("bot-users", "bot-user-created", whcb.GaCommon())
			gaEvent.Label = fmt.Sprintf("%v", botUserID)
			whcb.GaMeasurement().Queue(gaEvent)
		}
	} else {
		logger.Infof("Found existing bot user entity")
	}
	return botUser, err
}

func (whcb *WebhookContextBase) getChatEntityBase(whc WebhookContext) error {
	logger := whcb.Logger()
	if whcb.HasChatEntity() {
		logger.Warningf("Duplicate call of func (whc *bot.WebhookContext) _getChat()")
		return nil
	}

	botChatID := whc.BotChatID()
	logger.Infof("botChatID: %v", botChatID)
	botChatEntity, err := whcb.BotChatStore.GetBotChatEntityById(botChatID)
	switch err {
	case nil: // Nothing to do
		logger.Debugf("Loaded botChatEntity: %v", botChatEntity)
	case ErrEntityNotFound: //TODO: Should be this moved to DAL?
		err = nil
		logger.Infof("BotChat not found, first check for bot user entity...")
		botUser, err := whcb.GetOrCreateBotUserEntityBase()
		if err != nil {
			return err
		}

		botChatEntity = whcb.BotChatStore.NewBotChatEntity(botChatID, botUser.GetAppUserIntID(), botChatID, botUser.IsAccessGranted())

		if whc.GetBotSettings().Mode == Production {
			gaEvent := measurement.NewEvent("bot-chats", "bot-chat-created", whc.GaCommon())
			gaEvent.Label = fmt.Sprintf("%v", botChatID)
			whc.GaMeasurement().Queue(gaEvent)
		}

	default:
		return err
	}
	logger.Debugf(`chatEntity.PreferredLanguage: %v, whc.locale.Code5: %v, chatEntity.PreferredLanguage != """ && whc.locale.Code5 != chatEntity.PreferredLanguage: %v`, botChatEntity.GetPreferredLanguage(), whc.Locale().Code5, botChatEntity.GetPreferredLanguage() != "" && whc.Locale().Code5 != botChatEntity.GetPreferredLanguage())
	if botChatEntity.GetPreferredLanguage() != "" && whc.Locale().Code5 != botChatEntity.GetPreferredLanguage() {
		err = whc.SetLocale(botChatEntity.GetPreferredLanguage())
		if err == nil {
			logger.Debugf("whc.locale changed to: %v", whc.Locale().Code5)
		} else {
			logger.Errorf("Failed to set locate: %v")
		}
	}
	whcb.chatEntity = botChatEntity
	return err
}

func (whc *WebhookContextBase) AppUserEntity() BotAppUser {
	return whc.appUser
}

func (whc *WebhookContextBase) Context() context.Context {
	return appengine.NewContext(whc.r)
}

func (whcb *WebhookContextBase) NewMessageByCode(messageCode string, a ...interface{}) MessageFromBot {
	return MessageFromBot{Text: fmt.Sprintf(whcb.Translate(messageCode), a...), Format: MessageFormatHTML}
}

func (whcb *WebhookContextBase) NewMessage(text string) MessageFromBot {
	return MessageFromBot{Text: text, Format: MessageFormatHTML}
}

func (whcb WebhookContextBase) Locale() strongo.Locale {
	if whcb.locale.Code5 == "" {
		return whcb.BotContext.BotSettings.Locale
	}
	return whcb.locale
}

func (whcb *WebhookContextBase) SetLocale(code5 string) error {
	locale, err := whcb.botAppContext.SupportedLocales().GetLocaleByCode5(code5)
	if err != nil {
		whcb.logger.Errorf("WebhookContextBase.SetLocate() - %v", err)
		return err
	}
	whcb.locale = locale
	return nil
}
