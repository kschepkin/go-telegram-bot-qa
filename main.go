package main

import (
	"crypto/tls"
	"encoding/json"
	"flag"
	"fmt"
	tgbotapi "github.com/Syfaro/telegram-bot-api"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"
)

var (
	SiteList              map[string]int
	chatID                int64
	sslDaysToExipireAlert int64
	telegramBotToken      string
	configFile            string
	pprofListen           string
	HelpMsg               = "Это простой мониторинг доступности сайтов. Он обходит сайты в списке и ждет что он ответит 200, если возвращается не 200 или ошибки подключения, то бот пришлет уведомления в чат\n" +
		"Список доступных комманд:\n" +
		"/site_list - покажет список сайтов в мониторинге и их статусы (про статусы ниже)\n" +
		"/site_add [url] - добавит url в список мониторинга\n" +
		"/site_del [url] - удалит url из списка мониторинга\n" +
		"/help - отобразить это сообщение\n" +
		"\n" +
		"У сайтов может быть несколько статусов:\n" +
		"0 - никогда не проверялся (ждем проверки)\n" +
		"1 - ошибка подключения \n" +
		"2 - истекает сертификат \n" +
		"200 - ОК-статус" +
		"все остальные http-коды считаются некорректными"
)

func init() {
	SiteList = make(map[string]int)
	flag.StringVar(&configFile, "config", "config.json", "config file")
	flag.StringVar(&pprofListen, "pprofListen", ":6060", "Pprof listen interface")
	flag.StringVar(&telegramBotToken, "telegrambottoken", "", "Telegram Bot Token")
	flag.Int64Var(&chatID, "chatid", 79039545, "chatId to send messages")
	flag.Int64Var(&sslDaysToExipireAlert, "sslDaysToExipireAlert", 10, "SSL certificate expiration threshold")

	flag.Parse()

	if telegramBotToken == "" {
		log.Print("-telegrambottoken is required")
		os.Exit(1)
	}

	if chatID == 0 {
		log.Print("-chatid is required")
		os.Exit(1)
	}

	load_list()
}

func send_notifications(bot *tgbotapi.BotAPI) {
	for site, status := range SiteList {
		if status != 200 {
			alarm := fmt.Sprintf("CRIT - %s ; status: %v", site, status)
			bot.Send(tgbotapi.NewMessage(chatID, alarm))
		}
	}
}

func save_list() {
	data, err := json.Marshal(SiteList)
	if err != nil {
		panic(err)
	}
	err = ioutil.WriteFile(configFile, data, 0644)
	if err != nil {
		panic(err)
	}
}

func load_list() {
	data, err := ioutil.ReadFile(configFile)
	if err != nil {
		log.Printf("No such file - starting without config: %s", err)
		return
	}

	if err = json.Unmarshal(data, &SiteList); err != nil {
		log.Printf("Cant read file - starting without config: %s", err)
		return
	}
	log.Printf(string(data))
}

func monitor(bot *tgbotapi.BotAPI) {

	tr := &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
	}

	var httpclient = &http.Client{
		Timeout:   time.Second * 10,
		Transport: tr,
	}

	for {
		save_list()
		for site, _ := range SiteList {
			response, err := httpclient.Get(site)
			if err != nil {
				SiteList[site] = 1
				log.Printf("Status of %s: %s: %s", site, "1 - Connection error", err)
			} else {
				log.Printf("Status of %s: %s", site, response.Status)
				SiteList[site] = response.StatusCode

				siteUrl, err := url.Parse(site)
				if err != nil {
					panic(err)
				}
				if siteUrl.Scheme == "https" {
					conn, err := tls.Dial("tcp", siteUrl.Host+":443", tr.TLSClientConfig)
					if err != nil {
						log.Printf("Error in SSL dial to %s: %s", siteUrl.Host, err)
					}

					certs := conn.ConnectionState().PeerCertificates
					for _, cert := range certs {
						difference := time.Since(cert.NotAfter)
						daysToExprire := int64(difference.Hours() / 24)
						if daysToExprire > -(sslDaysToExipireAlert) {
							log.Printf("Status of %s: %s", site, "2 - certificate is expiring")
							SiteList[site] = 2
						}
					}
					conn.Close()
				}
			}
		}
		send_notifications(bot)
		time.Sleep(time.Minute * 5)
	}
}

func main() {
	// Server for pprof
	go func() {
		fmt.Println(http.ListenAndServe(pprofListen, nil))
	}()
	log.Printf("Pprof interface: %s", pprofListen)

	bot, err := tgbotapi.NewBotAPI(telegramBotToken)
	bot.Debug = true
	if err != nil {
		log.Panic(err)
	}

	log.Printf("Authorized on account %s", bot.Self.UserName)
	log.Printf("Config file: %s", configFile)
	log.Printf("ChatID: %v", chatID)
	log.Printf("Starting monitoring thread")
	go monitor(bot)

	u := tgbotapi.NewUpdate(0)
	u.Timeout = 60

	bot.Send(tgbotapi.NewMessage(chatID, fmt.Sprint("Я живой; вот сайты которые буду мониторить: ", SiteList)))

	updates, err := bot.GetUpdatesChan(u)

	for update := range updates {
		reply := ""
		if update.Message == nil {
			continue
		}

		log.Printf("[%s] %s", update.Message.From.UserName, update.Message.Text)

		switch update.Message.Command() {
		case "site_list":
			sl, _ := json.Marshal(SiteList)
			reply = string(sl)

		case "site_add":
			if update.Message.CommandArguments() != "" {
				SiteList[update.Message.CommandArguments()] = 0
				reply = "Site added to monitoring list"
			} else {
				reply = "Url is required"
			}

		case "site_del":
			if update.Message.CommandArguments() != "" {
				delete(SiteList, update.Message.CommandArguments())
				reply = "Site deleted from monitoring list"
			} else {
				reply = "Url is required"
			}

		case "help":
			reply = HelpMsg

		case "load_data":
			if update.Message.CommandArguments() != "" {
				reply = testdata(update.Message.CommandArguments())
			} else {
				reply = "Url is required"
			}
		}

		msg := tgbotapi.NewMessage(update.Message.Chat.ID, reply)
		bot.Send(msg)
	}

}

func testdata(url string) string {
	// Страница с которой хотим взять данные для теста

	// Регулярки для выборки
	var pcard = regexp.MustCompile(`data-url="(\/p\/.*)"`)
	var pcode = regexp.MustCompile(`code=(.*)" `)
	// Создаем переменные
	var products strings.Builder
	var tocart strings.Builder

	//добавляем https, если его не было, http не нужен, поэтому его в расчет не берем
	if !strings.Contains(url, "https://") {
		url = "https://" + url
	}

	// Получаем код страницы, сохраняем в переменную
	tr := &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
	}

	var client = &http.Client{
		Timeout:   time.Second * 10,
		Transport: tr,
	}

	resp, err := client.Get(url)

	if err != nil {
		fmt.Print(err)
	} else {
		defer resp.Body.Close()
	}
	bodyBytes, err := io.ReadAll(resp.Body)
	bodyString := string(bodyBytes)

	// Выборка ссылки на карточку товара
	for i, match := range pcard.FindAllString(bodyString, -1) {
		products.WriteString(strings.ReplaceAll((strings.ReplaceAll(match, "data-url=\"", "")), "\"", "\n"))
		if i < 0 {
			print(i)
		}
	}

	// Выборка коды товаров для добавления в корзину
	for i, match := range pcode.FindAllString(bodyString, -1) {
		tocart.WriteString(strings.ReplaceAll((strings.ReplaceAll(match, "code=", "\n")), "\"", ""))
		if i < 0 {
			print(i)
		}
	}
	result := products.String() + "\n----\n" + tocart.String()
	return result
}
