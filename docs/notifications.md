# Benachrichtigungskanäle einrichten

Alle Kanäle werden pro Account in den **Einstellungen** konfiguriert, nicht
über Umgebungsvariablen. Mehrere Kanäle können gleichzeitig aktiv sein – bei
einer passenden Show gehen alle aktivierten Kanäle gleichzeitig raus. Jeder
Kanal hat einen eigenen "Testen"-Button, der mit dem zuletzt gespeicherten
Stand eine Testbenachrichtigung schickt.

## Pushover

1. Account auf [pushover.net](https://pushover.net) anlegen, App auf dem
   Handy installieren.
2. Den **User Key** findest du direkt auf der Pushover-Startseite nach dem
   Login.
3. Unter [pushover.net/apps/build](https://pushover.net/apps/build) eine neue
   Application anlegen (Name frei wählbar, z.B. "BaseAlert") – daraus ergibt
   sich der **API Token**.
4. Beide Werte in den Einstellungen bei Pushover eintragen, aktivieren,
   Testen.

## ntfy

1. Einen Topic-Namen ausdenken – etwas Unrätbares wie `basealert-x7f2q`, denn
   öffentliche ntfy-Topics sind für jeden mit dem Namen mitlesbar.
2. **Server-URL**: entweder das öffentliche `https://ntfy.sh` verwenden, oder
   eine eigene ntfy-Instanz betreiben (`docker run -p 80:80 binwiederhall/ntfy serve`
   o.ä.) und deren URL eintragen.
3. **Zugriffstoken** nur nötig, falls das Topic auf der eigenen Instanz per
   ACL geschützt ist.
4. In der ntfy-App (iOS/Android/Web) das gleiche Topic abonnieren.

## Telegram

1. Mit [@BotFather](https://t.me/BotFather) in Telegram chatten, `/newbot`
   senden, Namen vergeben – BotFather gibt dir den **Bot Token**.
2. **Chat ID** herausfinden: dem neuen Bot einmal irgendeine Nachricht
   schreiben, dann im Browser
   `https://api.telegram.org/bot<TOKEN>/getUpdates` öffnen und den Wert bei
   `"chat":{"id": ...}` ablesen. Alternativ [@userinfobot](https://t.me/userinfobot)
   nach der eigenen ID fragen.
3. Bot Token + Chat ID eintragen.

## Discord

1. Im Discord-Server: Kanal-Einstellungen → Integrationen → Webhooks → Neuer
   Webhook, Kanal auswählen.
2. Die **Webhook-URL** kopieren und eintragen.

## Generischer Webhook

Für alles, was BaseAlert nicht direkt unterstützt (Home Assistant, n8n, IFTTT
Webhooks, ein eigenes Skript, ...). BaseAlert schickt einen `POST` mit
JSON-Body:

```json
{
  "title": "DJ Example legt gleich auf!",
  "message": "Show-Name auf TechnoBase.FM um 20:00 Uhr (Genre)",
  "url": "https://www.technobase.fm"
}
```

Einfach die URL eintragen, die diesen Payload entgegennimmt.

## E-Mail (SMTP)

Host, Port, Benutzer, Passwort, Absender- und Empfänger-Adresse eintragen.
STARTTLS ist standardmäßig aktiv (Port 587); bei Port 465 nutzt BaseAlert
automatisch eine direkte TLS-Verbindung statt STARTTLS.

Bei Gmail als Versender: erfordert 2FA auf dem Google-Konto plus ein
[App-Passwort](https://myaccount.google.com/apppasswords) (das normale
Account-Passwort funktioniert nicht mehr für SMTP), Host `smtp.gmail.com`,
Port `587`.
