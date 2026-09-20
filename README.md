# Squid Admin

Squid proxy serverini **terminalsiz** boshqarish paneli: foydalanuvchilar, bloklash, kirish qoidalari, tezlik va hajm
cheklovlari, statistika, ogohlantirishlar va Squid sozlamalari, hammasi brauzerdan.

Panel bitta fayl (Go binari, ichida veb-interfeys) va **Squid bilan bir serverda** ishlaydi. U `squid.conf` faylini
yozadi, `systemctl` va `squid -k` orqali Squid'ni boshqaradi, `access.log`ni o'qiydi. Boshqa kompyuterdagi Squid'ni
boshqarmaydi (bir nechta serverni boshqarish hozircha yo'q).

## Imkoniyatlar

| Bo'lim | Nima qiladi |
|---|---|
| **Xavfsiz o'zgartirish** | Har o'zgarish `squid -k parse` bilan tekshiriladi, reload'dan keyin Squid o'lsa config avtomatik qaytariladi, har versiya tarixda saqlanadi |
| **Squid sozlamalari** | Portlar, kesh, timeout, DNS, yuqori proksi va boshqalar formalar bilan; joriy qiymatlar `squid.conf`dan o'qiladi |
| **Kirish qoidalari** | ACL'lar, tartiblangan qoidalar, shablonlar (oq ro'yxat, kalit so'z, foydalanuvchiga bog'liq), tashqi blocklist manbalari; **siyosat ko'rinishi** va so'rov **sinovchisi** ("nega bu so'rov o'tdi/bloklandi?") |
| **Proxy foydalanuvchilari** | Qo'shish, o'chirmasdan to'xtatish, amal qilish muddati, kunlik hajm kvotasi, guruhlar, CSV import/eksport |
| **Cheklovlar** | Tezlik (`delay_pools`) va bitta fayl hajmi (`reply_body_max_size`) |
| **Statistika** | `access.log`dan trafik, keshdan olinganlar, bloklashlar, top saytlar/qurilmalar/foydalanuvchilar, jonli Squid ma'lumotlari, disk, log aylantirish |
| **Ogohlantirishlar** | Squid to'xtasa, disk to'lsa, bloklashlar yoki xatolar ko'paysa: Telegram, webhook, e-pochta |
| **Boshqaruv** | Rollar (kuzatuvchi / operator / admin), audit log, sessiyalar, majburiy parol almashtirish, blokirovka |

## Talablar

- Linux, **systemd**, o'rnatilgan va ishlab turgan **Squid**. Sinalgan: Debian + Squid 7.6. Ubuntu va RHEL oilasi
  uchun o'rnatuvchi tayyorlangan (yo'llar va guruhlar aniqlanadi), lekin ular hali sinalmagan
- `htpasswd` (`apache2-utils` yoki `httpd-tools`), `sudo`
- Yig'ish uchun (serverda yoki boshqa joyda): **Go** va **Node.js**

## O'rnatish

Ishlab turgan Squid'ingiz bo'lsa, avval **zaxira oling**: `sudo cp -a /etc/squid /root/squid-backup`.

**A) Serverning o'zida yig'ib o'rnatish**
```bash
git clone https://github.com/DiorDevv/Terminal.git && cd Terminal
sudo ./deploy/install.sh
```

**B) Boshqa kompyuterda yig'ib, tayyor faylni serverga ko'chirish** (serverda Go va Node kerak emas)
```bash
./scripts/build.sh                      # bin/squidadmin-backend hosil bo'ladi
scp bin/squidadmin-backend deploy/install.sh server:/tmp/
ssh server 'sudo /tmp/install.sh --bin /tmp/squidadmin-backend'
```

O'rnatuvchi (`deploy/install.sh`) quyidagilarni qiladi va qayta ishga tushirganda ham xavfsiz (idempotent):

- `squidadmin` nomli **root bo'lmagan** tizim foydalanuvchisini yaratadi va uni Squid guruhiga qo'shadi (logni o'qish uchun)
- `squid.conf`, blocklist va passwd fayllariga shu foydalanuvchiga yozish huquqi beradi; `squid.conf.orig` nusxasini oladi
- `/etc/sudoers.d/squidadmin` ga **aniq belgilangan** bir nechta buyruqni yozadi (`systemctl start/stop/restart/reload/enable/disable squid`, `squid -k rotate`), boshqa hech narsani emas
- `squidadmin` systemd xizmatini o'rnatadi (cheklangan: `ProtectSystem`, `ProtectHome`, `PrivateTmp` va boshqalar) va ishga tushiradi
- sozlamalar `/etc/squidadmin/env` da, ma'lumotlar bazasi `/var/lib/squidadmin/` da

**Birinchi kirish:** panel `http://SERVER:8080` da ochiladi. Birinchi admin parolini (bir marta ko'rsatiladi) shu
buyruq bilan oling va kirgach darhol almashtiring:
```bash
sudo journalctl -u squidadmin -o cat | grep -A2 username
```

## HTTPS (ochiq tarmoq uchun majburiy)

Panelning o'zi oddiy HTTP'da gaplashadi. Parollar tarmoqda ochiq ketmasligi uchun uni **TLS bilan reverse proxy**
orqasiga qo'ying va faqat lokal manzilda tinglating.

`/etc/squidadmin/env` ga qo'shing:
```
BIND_ADDR=127.0.0.1
COOKIE_SECURE=true
```
keyin `sudo systemctl restart squidadmin`.

**Caddy** (sertifikatni o'zi oladi va yangilaydi; WebSocket avtomatik ishlaydi):
```
panel.sizning-domen.uz {
    reverse_proxy 127.0.0.1:8080
}
```

**nginx:**
```nginx
server {
    listen 443 ssl;
    server_name panel.sizning-domen.uz;
    # ssl_certificate / ssl_certificate_key ...

    location / {
        proxy_pass http://127.0.0.1:8080;
        proxy_set_header Host $host;                       # origin tekshiruvi uchun shart
        proxy_set_header X-Forwarded-For $remote_addr;
        proxy_set_header X-Forwarded-Proto $scheme;
        proxy_http_version 1.1;
        proxy_set_header Upgrade $http_upgrade;            # jonli loglar (WebSocket)
        proxy_set_header Connection "upgrade";
        proxy_read_timeout 300s;                           # Squid'ni qayta ishga tushirish uzoq davom etadi
    }
}
```
Proxy `Host` sarlavhasini o'zgartirsa, `FRONTEND_ORIGIN=https://panel.sizning-domen.uz` ni ham belgilang.
Panel `X-Forwarded-For`ga faqat `TRUSTED_PROXIES` (odatda `127.0.0.1`) dan ishonadi.

## Sozlamalar (`/etc/squidadmin/env`)

| O'zgaruvchi | Standart | Ma'nosi |
|---|---|---|
| `PORT` / `BIND_ADDR` | `8080` / hammasi | Qaysi manzilda tinglaydi |
| `DB_PATH` | `./data/squidadmin.db` | Ma'lumotlar bazasi (o'rnatuvchi `/var/lib/squidadmin` qo'yadi) |
| `FRONTEND_ORIGIN` | `http://localhost:5173` | Qo'shimcha ruxsat etilgan brauzer manzillari (panel o'z manzilini o'zi ruxsat etadi) |
| `COOKIE_SECURE` | `false` | HTTPS orqasida `true` |
| `TRUSTED_PROXIES` | `127.0.0.1,::1` | `X-Forwarded-For`ga ishoniladigan proxylar |
| `SQUID_CONF_PATH` | `/etc/squid/squid.conf` | Squid konfiguratsiyasi |
| `SQUID_ACCESS_LOG` / `SQUID_LOG_DIR` | `/var/log/squid/access.log` | Statistika manbai / log papkasi |
| `SQUID_BIN` / `SQUID_SERVICE` / `SYSTEMCTL_BIN` | `squid` | Buyruq va xizmat nomlari |
| `SQUID_MANAGER` / `SQUID_USE_SUDO` | `auto` / `false` | `systemd` yoki `direct`; sudo orqali ishga tushirish (o'rnatuvchi `true` qo'yadi) |
| `SQUID_PASSWD_PATH`, `SQUID_BLACKLIST_PATH`, `SQUID_BLOCKLIST_DIR` | `/etc/squid/...` | Panel boshqaradigan fayllar |
| `SQUID_MANAGER_URL` | `http://127.0.0.1:3128/squid-internal-mgr/info` | Jonli Squid ma'lumotlari |
| `STATS_RETENTION_DAYS` | `90` | Statistika saqlanadigan muddat |
| `ALERT_CHECK_SECONDS` / `POLICY_CHECK_SECONDS` | `60` / `30` | Ogohlantirish va muddat/kvota tekshiruvi oralig'i |
| `MONITOR_DISK_PATHS` | log va DB papkasi | Diski kuzatiladigan yo'llar |
| `ADMIN_USERNAME` / `ADMIN_PASSWORD` | `admin` / tasodifiy | Birinchi admin (parol berilmasa, bir marta chop etiladi) |

To'liq ro'yxat va izohlar: `backend/.env.example`.

## Zaxira nusxa va tiklash

Muhim narsalar: `/etc/squid/` (Squid sozlamalari, foydalanuvchilar), `/etc/squidadmin/env` va
`/var/lib/squidadmin/squidadmin.db` (panel foydalanuvchilari, cheklovlar, tarix, statistika).

```bash
sudo systemctl stop squidadmin                       # baza yozilmayotgan paytda
sudo tar czf squid-backup-$(date +%F).tgz /etc/squid /etc/squidadmin /var/lib/squidadmin
sudo systemctl start squidadmin
```
Tiklash: fayllarni joyiga qo'yib, `sudo systemctl restart squid squidadmin`. Config ichidagi xatoni esa panelning
**Config tarixi** sahifasidan terminalsiz qaytarish mumkin.

## Yangilash va o'chirish

```bash
git pull && sudo ./deploy/install.sh      # sozlamalar va ma'lumotlar saqlanib qoladi
sudo ./deploy/install.sh --uninstall      # xizmatni, sudoers'ni, foydalanuvchini olib tashlaydi (ma'lumotlar qoladi)
```

## Muammolarni hal qilish

| Belgi | Nima qilish kerak |
|---|---|
| Panel ochilmaydi | `sudo systemctl status squidadmin`, `sudo journalctl -u squidadmin -n 50` |
| "Interfeys yig'ilmagan" sahifasi | Binar `scripts/build.sh`siz qurilgan; shu skript bilan qayta yig'ing |
| O'zgarish "reload xatosi" bilan qaytdi | Xato matni ekranda; Squid o'zi avvalgi ishlaydigan configga qaytdi |
| Statistika yangilanmaydi / "log o'qilmayapti" | `squidadmin` foydalanuvchisi Squid guruhida bo'lishi kerak: `id squidadmin`, log fayl guruhi `ls -l /var/log/squid` |
| "Squid'ning jonli ma'lumotlarini olib bo'lmadi" | Cache manager faqat `localhost`dan ochiq; `SQUID_MANAGER_URL` va `http_port`ni tekshiring |
| Kirishdan keyin darhol chiqarib yuboradi | HTTPS orqasida `COOKIE_SECURE=true` va `Host` sarlavhasi to'g'ri uzatilganini tekshiring |
| Kvota bloklashi kechikadi | Hajm `access.log`dan hisoblanadi: kvota to'lgach 30–60 soniya ichida bloklanadi |

## Ishlab chiqish

```bash
cd backend  && go run ./cmd/server                 # API :8080 (SQUID_* yo'llarini .env da bering)
cd frontend && npm ci && npm run dev               # interfeys :5173, /api ni :8080 ga uzatadi
```
Tekshiruvlar:
```bash
cd backend  && go vet ./... && go test ./...
cd frontend && npx tsc -b && npx oxlint && npm test && npm run build
```
Haqiqiy Squid bilan avtomatik sinovlar (`root` sifatida, panel o'rnatilgan mashinada): `deploy/e2e/phase2.py` ...
`phase6.py`. Ularning hujjati har bir fayl boshida.

Arxitektura va ishlab chiqish rejasi: [`ROADMAP.md`](ROADMAP.md).

## Xavfsizlik modeli (qisqacha)

- Panel `squidadmin` (root emas) sifatida ishlaydi; sudoers'da faqat aniq buyruqlar.
- Sessiyalar serverda, cookie `HttpOnly` va `SameSite=Strict`, CSRF sarlavhasi, WebSocket origin tekshiruvi.
- Parollar `bcrypt`, urinishlar chegaralangan, ma'lumotlar bazasi `0600`.
- Alert webhook/SMTP manzillari ichki tarmoqqa (loopback, bulut metadata) yo'naltirilishdan himoyalangan.
- **`admin` roli konfiguratsiya orqali Squid nomidan yordamchi dasturlar ishga tushira oladi**, shuning uchun admin
  rolini ishonchli odamlarga bering.
- Muhim cheklovlar: 2FA yo'q, sirlar (Telegram token, SMTP paroli) bazada shifrlanmagan.
