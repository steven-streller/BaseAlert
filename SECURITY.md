# Security Policy

## Unterstützte Versionen

BaseAlert befindet sich in aktiver Entwicklung (aktuell < 1.0.0). Sicherheitsupdates
werden ausschließlich für die jeweils neueste veröffentlichte Version bereitgestellt.

| Version | Unterstützt        |
| ------- | ------------------ |
| Neueste `0.x` Release | :white_check_mark: |
| Ältere Versionen      | :x:                 |

## Sicherheitslücke melden

Bitte meldet Sicherheitslücken **nicht** über öffentliche GitHub Issues.

Nutzt stattdessen einen der folgenden Wege:

- **GitHub Security Advisories** (bevorzugt): über den Reiter
  ["Security" → "Report a vulnerability"](https://github.com/steven-streller/BaseAlert/security/advisories/new)
  im Repository. So bleibt die Meldung privat, bis ein Fix verfügbar ist.
- **E-Mail**: [steven.streller@googlemail.com](mailto:steven.streller@googlemail.com)

Bitte gebt so viele Details wie möglich an, u. a.:

- Betroffene Version bzw. Commit
- Art der Schwachstelle (z. B. XSS, SQL-Injection, Auth-Bypass)
- Schritte zur Reproduktion bzw. ein Proof of Concept
- Mögliche Auswirkungen

## Ablauf

- Ihr erhaltet innerhalb von 3 Werktagen eine Rückmeldung zum Eingang der Meldung.
- Wir bewerten die Schwere und melden uns mit einer Einschätzung, in der Regel
  innerhalb von 7 Tagen.
- Nach einem Fix wird koordiniert offengelegt (Coordinated Disclosure); auf Wunsch
  wird bei der Veröffentlichung des Advisories auf euch als Melder verwiesen.
- Unbegründete oder nicht sicherheitsrelevante Meldungen werden als solche
  gekennzeichnet und ggf. an den regulären Issue-Tracker verwiesen.

## Umfang

Diese Policy deckt den Code in diesem Repository ab (BaseAlert-Anwendung,
Docker-Images, GitHub-Actions-Workflows). Schwachstellen in Drittanbieter-
Abhängigkeiten bitte direkt beim jeweiligen Projekt melden; Hinweise darauf
nehmen wir hier trotzdem gerne entgegen, z. B. wenn eine bekannte CVE in
diesem Projekt noch nicht gepatcht wurde.
