# Перший запуск деплою через GitHub Actions

Ця інструкція описує одноразове налаштування `visionp/report-hosting` на спільному
VPS. Після підготовки сервера кожен push у `main` запускає перевірки, збірку,
передачу через SSH та оновлення застосунку.

Бекапи та автоматичний відкат у цьому процесі не реалізовані.

## 1. Додати secrets у GitHub

Відкрити [Settings → Secrets and variables → Actions](https://github.com/visionp/report-hosting/settings/secrets/actions)
і через **New repository secret** додати чотири значення:

| Назва | Що вказати |
| --- | --- |
| `SERVER_HOST` | IP або адреса того самого VPS, де працюють інші проєкти |
| `SERVER_USER` | SSH-користувач для деплою |
| `SERVER_SSH_KEY` | Приватний SSH-ключ цього користувача |
| `SERVER_FINGERPRINT` | Відбиток SSH-ключа сервера у форматі `SHA256:…` |

Перші три значення можуть бути такими самими, як у сусідніх проєктах. Secrets
іншого репозиторію автоматично тут не з'являться: додати їх окремо або надати
цьому репозиторію доступ до відповідних secrets організації. Публічна частина
SSH-ключа має бути дозволена на VPS для `SERVER_USER`.

Fingerprint отримати через консоль VPS або вже довірене SSH-з'єднання:

```bash
sudo ssh-keygen -lf /etc/ssh/ssh_host_ed25519_key.pub -E sha256
```

Скопіювати саме поле `SHA256:…`. Workflow використовує SSH-порт `22` і перевіряє
цей відбиток як під час передачі файлів, так і під час запуску деплою.
Приватні ключі та реальні значення secrets у репозиторій не додавати.

## 2. Отримати першу збірку

У [GitHub Actions](https://github.com/visionp/report-hosting/actions) вибрати
**Checks → Run workflow → main**. Цей workflow виконує перевірки та збірку без
підключення до VPS.

Після успішного завершення завантажити артефакт `report-hosting-<commit>`.
Він містить `report_hosting` і `SHA256SUMS`. Для першого встановлення перенести
ці файли на VPS разом із файлами `deploy/` тієї самої версії репозиторію.
Розпакувати артефакт у каталог `out/` робочої копії та перевірити його:

```bash
(cd out && sha256sum --check --strict SHA256SUMS)
```

Застосунок збирається для Linux x86-64. На VPS команда `uname -m` має показати
`x86_64`. Go та Docker на production-сервері для цього деплою не потрібні.

## 3. Один раз підготувати VPS

Команди першого встановлення наведені в [RUNBOOK.md](RUNBOOK.md), а налаштування
каталогів для завантаження збірок і sudo — у [GITHUB_ACTIONS.md](GITHUB_ACTIONS.md).
Виконати обидві частини. Конфігурація розрахована на Debian/Ubuntu із systemd.

Потрібно:

- Створити службового користувача `report_hosting` і каталоги даних під
  `/var/lib/report_hosting` із правами, наведеними в RUNBOOK.
- Встановити перший виконуваний файл як `/usr/local/bin/report_hosting`.
- Створити `/etc/report_hosting/config.json` на основі
  [config.example.json](config.example.json), замінивши `REPLACE…` реальними
  налаштуваннями Cloudflare Access.
- Створити `/etc/report_hosting/report_hosting.env`; він може бути порожнім,
  якщо всі налаштування задані в JSON.
- Встановити [systemd-сервіс](systemd/report_hosting.service), виконати
  `systemctl daemon-reload` та увімкнути сервіс для запуску після перезавантаження.
- Встановити [скрипт деплою](scripts/deploy-release.sh) як
  `/usr/local/sbin/report-hosting-deploy`, власник `root`, права `0755`.
- Дати SSH-користувачу запис у `/opt/report-hosting/releases`.
- Створити `/var/lib/report_hosting-deploy`, власник `root`, права `0700`.
- Дозволити SSH-користувачу виконувати встановлений скрипт через `sudo` без
  пароля. Приклад правила наведений нижче; `deploy` замінити на `SERVER_USER`.
- Перевірити через `sudo ss -ltnp`, що порт `127.0.0.1:5193` не зайнятий іншим
  сервісом. Встановити потрібні команди: `curl`, `python3`, `flock` із `util-linux`.

Правило для `/etc/sudoers.d/report-hosting-deploy` створити через `visudo`:

```sudoers
deploy ALL=(root) NOPASSWD: /usr/local/sbin/report-hosting-deploy
```

Сам скрипт, systemd unit та конфігурація мають залишатися під контролем `root`.
Детальні команди встановлення, права і перевірка sudoers — у
[GITHUB_ACTIONS.md](GITHUB_ACTIONS.md).

## 4. Налаштувати домен і доступ

Поточний застосунок і Nginx-конфіг передбачають:

- Зону `partnersflex.space` у Cloudflare та проксований wildcard DNS-запис
  `*.partnersflex.space`, спрямований на IP VPS.
- Режим SSL/TLS **Full (strict)** та Origin Certificate для
  `partnersflex.space` і `*.partnersflex.space`.
- Власний клієнтський сертифікат для zone-level Authenticated Origin Pulls.
  Наявний Nginx-конфіг вимагає його перевірки.
- Cloudflare Access для `admin.partnersflex.space` із доступом для адміністратора.
- Реальні `access_team_domain` і `access_aud` у конфігурації застосунку.
- Правило обходу кешу для захищених сайтів `*.partnersflex.space`.

Сертифікати встановити за шляхами з [nginx/tls.conf](nginx/tls.conf):
`/etc/report_hosting/tls/origin.pem`, `origin.key`, `zone-client-ca.pem`.
Встановити Nginx-конфіг із RUNBOOK, перевірити `sudo nginx -t` і перечитати
конфігурацію. Зберегти налаштування інших сайтів та поточний default virtual host.

Створити адміністратора застосунку; пароль вводиться інтерактивно:

```bash
sudo -u report_hosting /usr/local/bin/report_hosting admin create -config /etc/report_hosting/config.json administrator
```

Повний порядок налаштування Cloudflare, Nginx і запуску сервісу є в
[RUNBOOK.md](RUNBOOK.md).

## 5. Запустити та перевірити деплой

Після підготовки VPS у [GitHub Actions](https://github.com/visionp/report-hosting/actions)
вибрати **Deploy → Run workflow → main**.

Workflow має успішно виконати перевірки, збірку, передачу файлів і встановлення.
На VPS перевірити:

```bash
sudo systemctl is-active report_hosting
sudo cat /var/lib/report_hosting-deploy/current-release
sudo journalctl -u report_hosting -n 50 --no-pager
curl --noproxy '*' -i -H 'Host: admin.partnersflex.space' http://127.0.0.1:5193/login
```

Очікується статус сервісу `active`. Локальний HTTP-запит без Cloudflare Access
JWT має повернути `403` із текстом `access denied`: це перевірка захисту й запуску
застосунку. Окремо відкрити `https://admin.partnersflex.space`, пройти Cloudflare
Access і вхід адміністратора. Перевірити, що сусідні сервіси VPS також працюють.

Наступні push у `main` запускають деплой автоматично. Якщо перевірка або запуск
неуспішні, workflow завершиться помилкою; потрібно переглянути лог і повторити
деплой після виправлення. Скрипт не створює бекапів і не повертає попередню версію.

## Довідка

- [GitHub: secrets у GitHub Actions](https://docs.github.com/en/actions/how-tos/write-workflows/choose-what-workflows-do/use-secrets)
- [Cloudflare: zone-level Authenticated Origin Pulls](https://developers.cloudflare.com/ssl/origin-configuration/authenticated-origin-pull/set-up/zone-level/)
