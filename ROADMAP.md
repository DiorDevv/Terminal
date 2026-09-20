# Squid Admin — rivojlantirish rejasi

**Maqsad:** administrator Squid'ni boshqarish uchun hech qachon terminalga kirmasligi kerak. O'rnatishdan
tortib monitoringgacha hamma narsa paneldan bajariladi, har bir o'zgarish xavfsiz (tekshiriladi,
tarixi saqlanadi, buzilsa avtomatik qaytariladi).

Taxmin: panel **bitta Linux serverdagi bitta Squid**'ni boshqaradi (ko'p serverli rejim 8-bosqichda).

## Asosiy arxitektura tamoyillari

1. **Har bir o'zgarish tranzaksiya.** `Manager.Update`: qulf → o'qish → o'zgartirish → `squid -k parse` bilan
   tekshirish → atomik yozish → tarixga saqlash. Keyin `Reconfigure`: qayta yuklash → Squid tirikligini
   tekshirish → buzilgan bo'lsa **oxirgi ishlagan versiyaga avtomatik qaytish**.
2. **Qoidalar tartibi muhim.** Squid'da birinchi mos kelgan `http_access` ishlaydi. Boshqariladigan bloklar
   har doim to'g'ri joyga qo'yiladi (rad etish qoidalari birinchi `allow` dan oldin).
3. **Ma'lumot manbai — DB.** `squid.conf`ning boshqariladigan qismlari DB'dan generatsiya qilinadi va
   belgilangan marker bloklari ichida yashaydi; qolgan qism (qo'lda yozilgan) daxlsiz qoladi.
4. **Hech qanday jimgina xato.** Har bir amal natijasi (saqlandi / qayta yuklandi / qaytarildi) foydalanuvchiga
   ko'rsatiladi.

## Bosqichlar

### 1-bosqich — Poydevor va xizmatni boshqarish  ✅ (shu bosqichda bajarildi)
- [x] Tranzaksion `Manager.Update` + mutex (poyga sharoiti yo'q) + atomik fayl yozish
- [x] Config tarixi (DB), diff, bir tugma bilan rollback
- [x] Reload'dan keyin Squid tirikligini tekshirish va avtomatik rollback
- [x] **Xato tuzatildi:** vaqt cheklovlari `allow localhost` dan keyin turgani uchun ishlamas edi
- [x] Xizmatni boshqarish: start / stop / restart / reload / enable / disable, versiya, uptime, xotira
- [x] SQLite `foreign_keys` yoqildi, bo'sh ro'yxatlar `null` emas `[]` qaytaradi
- [x] CORS'ga `DELETE` qo'shildi, `FRONTEND_ORIGIN` sozlanadigan bo'ldi
- [x] Proksi foydalanuvchi paroli `htpasswd -i` (stdin) orqali beriladi (`ps` da ko'rinmaydi)

### 2-bosqich — Xavfsizlik (production uchun majburiy)  ✅ (bajarildi)
- [x] Standart sirlar yo'q: `JWT_SECRET` butunlay olib tashlandi (sessiyalar serverda), birinchi admin uchun
      tasodifiy bir martalik parol; eski `admin123` hisoblar kirishda parol almashtirishga majbur
- [x] Backend `root`dan chiqdi: alohida `squidadmin` foydalanuvchisi, qat'iy buyruqlar ro'yxati bilan `sudoers`,
      `deploy/install.sh` (idempotent, `--uninstall` bilan)
- [x] Token URL'dan va `localStorage`dan ketdi: `HttpOnly` + `SameSite=Strict` cookie, WebSocket cookie bilan
      autentifikatsiya qilinadi, `CheckOrigin` cheklangan (cross-site WebSocket hijacking yopildi)
- [x] Chiqishda sessiya haqiqatan bekor qilinadi; bo'sh turish (8 soat) va mutlaq (7 kun) muddat; faol sessiyalar
      ro'yxati va ularni tugatish
- [x] CSRF himoyasi (`X-Requested-With` + Origin tekshiruvi), CORS faqat ruxsat etilgan originlarga
- [x] Login: IP bo'yicha limit + hisob bloklash (5 xato → 15 daqiqa), foydalanuvchi mavjudligi vaqt bo'yicha sezilmaydi;
      butun API uchun foydalanuvchi bo'yicha rate limit; soxta `X-Forwarded-For` e'tiborga olinmaydi
- [x] Rollar: `viewer` / `operator` / `admin`, ko'p panel foydalanuvchisi, "oxirgi admin" himoyasi, parolni tiklash
- [x] Audit log: har bir o'zgartirish va kirish urinishi (rad etilganlari ham), parollar hech qachon yozilmaydi
- [ ] Ikki bosqichli autentifikatsiya (TOTP) — keyinga
- [ ] HTTPS: reverse proxy (nginx/Caddy) orqali; `COOKIE_SECURE=true` — 8-bosqichdagi Docker/nginx bilan birga

### 3-bosqich — Asosiy Squid sozlamalari (formalar orqali)  ✅ (bajarildi)
- [x] "Squid sozlamalari" sahifasi (sxemaga asoslangan, bo'limlar: Tarmoq / Kesh / Timeout'lar / Yuqori proksi):
      `http_port`, `visible_hostname`, `dns_nameservers`, `forwarded_for`, `via`, `cache_mem`, `maximum_object_size`,
      `cache_dir`, `connect_timeout`, `read_timeout`, `request_timeout`, `shutdown_lifetime`, `cache_peer`, `never_direct`
- [x] Saqlashdan oldin **ko'rib chiqish** (dry run: har bir sozlama "avval → keyin", Squid'ning o'z parser'i bilan tekshirilgan)
- [x] Maydon bo'yicha validatsiya (barcha xato bir vaqtda), kanonik yozuv (`256mb` → `256 MB`), `never_direct` uchun
      "yuqori proksisiz yoqib bo'lmaydi" qoidasi, `cache_peer` da parol (`login=`) rad etiladi
- [x] "Asl holatiga qaytarish": stock qator izohga olinadi (`# squidadmin-disabled: ...`) va qaytarilganda **bayt-baytiga**
      tiklanadi (haqiqiy 3000 qatorli config'da ham tekshirilgan)
- [x] `cache_dir` restart talab qiladi: reload qilinmaydi, "restart kerak" belgisi (Dashboard + sahifa), **tekshiruvli restart**
      (`POST /squid/settings/restart`): Squid ko'tarilmasa config avtomatik tiklanadi va Squid qayta yoqiladi
- [x] Rollar: ko'rish — operator, o'zgartirish va restart — admin; hamma o'zgarish audit va config tarixiga yoziladi

**Arxitektura qarori (rejadan farq):** sozlamalar DB'da emas, `squid.conf`ning o'zida (marker blok) saqlanadi. Sabab:
tarixdan versiya tiklansa yoki fayl qo'lda tahrirlansa, panel Squid haqiqatda ishlatayotgan narsani ko'rsatadi
(DB bilan ikki manba bo'lib qolmaydi).

**Ataylab qoldirilgan (sabab bilan):**
- `refresh_pattern` — tartibga bog'liq regex ro'yxati, 4-bosqichdagi qoidalar dvigateli bilan birga bo'lishi kerak
- Keshni tozalash (purge) — root/proxy huquqi kerak (maxsus imtiyozli yordamchi); `squid -z` esa systemd
  `ExecStartPre` orqali restart paytida o'zi bajariladi
- O'rnatish ustasi (Squid'ni o'rnatish) — panel root'siz ishlaydi; 8-bosqichdagi Docker/o'rnatuvchi bilan birga

### 4-bosqich — Kirish boshqaruvi dvigateli  ✅ (bajarildi)
- [x] Universal ACL quruvchi: `src`, `dst`, `dstdomain`, `dstdom_regex`, `url_regex`, `urlpath_regex`, `browser`,
      `port`, `method`, `time`, `proxy_auth` (+ `blocklist`, panel o'zi yaratadi). Har bir tur qiymatlari tekshiriladi va
      kanonik yoziladi; regexlar POSIX cheklovi bilan (lookahead, `\d` rad etiladi)
- [x] **Tartiblangan qoidalar** (allow/deny, "emas" sharti, yoqish/o'chirish, o'rnini o'zgartirish, istalgan joyga qo'shish);
      `http_access` bloki `squid.conf`ga generatsiya qilinadi
- [x] **Qat'iy blok tartibi** (bir nechta panel bloki bir xil nuqtaga qo'shilganda tartib tasodifiy bo'lib qolmasligi uchun):
      stock xavfsizlik denylari → blacklist → vaqt cheklovlari → **foydalanuvchi qoidalari** → stock allow'lar → LAN/auth → deny all
- [x] Foydalanuvchi bo'yicha qoidalar (`proxy_auth`: Squid'ning asosiy autentifikatsiyasida guruh yo'q, shuning uchun
      foydalanuvchilar ro'yxatiga ega ACL = guruh), IP guruhidan ACL'ga nusxalash
- [x] Shablonlar: **oq ro'yxat rejimi**, **kalit so'z bilan bloklash**, **saytni foydalanuvchilarga bog'lash**
- [x] **Tashqi blocklist'lar**: hosts / adblock (`||domain^`) / oddiy ro'yxat formatlari, ota-domen qoplagan qatorlarni
      olib tashlash, jadval bo'yicha avtomatik yangilash (bitta reload), o'zgarmasa qayta yozilmaydi, bo'sh/xato yuklash
      ishlayotgan ro'yxatni buzmaydi, **SSRF himoyasi** (loopback / link-local / metadata rad etiladi), hajm chegarasi
- [x] **Samarali siyosat** ko'rinishi (stock + `include` + barcha panel bloklari, qaysi qoida kimniki) va **"nima uchun?"
      tekshirgichi**: so'rovni Squid kabi tartib bilan baholaydi, 407 (login so'raladi) va "aniqlab bo'lmadi"ni ham ko'rsatadi
- [x] **Oracle E2E** (`deploy/e2e/phase4.py`): panel bashorati haqiqiy Squid javobi bilan 24 so'rovda solishtiriladi
      (manba IP, foydalanuvchi, usul, port, User-Agent, vaqt oynasi, CONNECT); hafta kunlari harflari va oyna chegaralari ham

**Haqiqiy Squid'da topilgan va hisobga olingan xususiyatlar:**
- `proxy_auth` ACL `auth_param` qatoridan **oldin** e'lon qilinsa Squid xato beradi → qoidalar bloki o'zining `auth_param`
  nusxasini olib yuradi (takrorlash zararsiz), autentifikatsiya sozlanmagan bo'lsa aniq xabar beriladi
- qoida ichida `proxy_auth` **birinchi** tursa, Squid hamma trafikdan login so'raydi → panel uni avtomatik **oxirga** qo'yadi
- `reload` ~40 ms da qaytadi, qoida ~70 ms keyin kuchga kiradi (Squid'ning asinxron reconfigure'i)

**Ataylab qoldirildi:**
- `mime_type` ACL: javob tomoniga tegishli (`http_reply_access`), `http_access` da ishlamaydi
- qoidalarni sichqoncha bilan tortish: hozir yuqoriga/pastga tugmalari (mantiq bir xil, `PUT /squid/access/order`)
- `refresh_pattern` (kesh tuzatish): qoidalar dvigateliga bog'liq emas, 5/8-bosqich bilan birga

### 5-bosqich — Monitoring va statistika  ✅ (bajarildi)
**Nima qilindi**
- `access.log` ni real vaqtda o'qib soatlik agregatga yig'adi (soat × qurilma × foydalanuvchi × domen): so'rovlar, trafik, keshdan (HIT), bloklangan (403 `TCP_DENIED`), 5xx xatolar. 407 login so'rovlari sanalmaydi (aks holda har autentifikatsiyalangan so'rov ikki marta chiqardi). Saqlash muddati `STATS_RETENTION_DAYS` (90 kun)
- Log aylantirilganda yoki panel qayta ishga tushganda **hech narsa yo'qolmaydi va ikki marta sanalmaydi**: ochiq fayl deskriptori + `os.SameFile`, holat birinchi qator barmoq izi + offset bilan saqlanadi
- `/stats` sahifasi (operator+): 6 ta ko'rsatkich, vaqt grafigi (24 soat / 7 / 30 kun; jadval ko'rinishi ham bor), eng ko'p so'ralgan saytlar / qurilmalar / foydalanuvchilar, so'nggi bloklanganlar, **Squid hozir** (`mgr:info`: versiya, mijozlar, kesh %, xotira, CPU, FD), disk, log o'quvchi holati, log fayllar ro'yxati va oxirgi 200 qator
- Log aylantirish tugmasi (admin): `squid -k rotate`; `logfile_rotate` sozlamasi Squid sozlamalariga qo'shildi (0–365); sudoers'ga aynan bitta yangi qator: `squid -k rotate`
- `/alerts` sahifasi (admin): Squid to'xtadi (ketma-ket 2 tekshiruv), disk to'ldi, bloklashlar ko'paydi, 5xx ko'paydi. Kanallar: Telegram, webhook, e-pochta (SMTP). Bir muammo = bir xabar; davom etsa 6 soatda eslatma; hal bo'lganda "hal bo'ldi". Tarix, sinov xabari, "hozir tekshirish"
- Sirlar (bot tokeni, webhook manzili, SMTP paroli) API orqali **hech qachon qaytarilmaydi** (faqat "saqlangan" belgisi), bo'sh qoldirilsa eskisi saqlanadi, xato xabarlariga ham tushmaydi
- `netguard` paketi: webhook, SMTP va blocklist yuklash uchun umumiy SSRF himoyasi (loopback, link-local/bulut metadata, unspecified, multicast — ulanish vaqtida, DNS/redirect orqali aylanib o'tib bo'lmaydi)

**Tekshiruv:** Go testlari (monitor, netguard, squid, api), mutatsion tekshiruv (18 mutant: 17 o'ldirildi, 1 ekvivalent — Q-kodlash CR/LF'ni o'zi neytrallaydi), haqiqiy Squid bilan `deploy/e2e/phase5.py` (panel raqamlari access.log'ni mustaqil o'qigan oracle bilan **aynan** teng: oddiy holat, panel restarti, haqiqiy `squid -k rotate`; haqiqiy webhook'ga yetkazish; Squid haqiqatan to'xtatilib ishga tushirildi), sahifalar Chrome'da ochilib ko'zdan kechirildi

**Ataylab qoldirildi / ma'lum cheklovlar**
- Telegram va SMTP haqiqiy serverga qarshi sinalmagan (Telegram — soxta API server, SMTP — soxta SMTP server bilan); webhook — haqiqiy tinglovchi bilan sinaldi
- Sirlar DB'da shifrlanmagan holda saqlanadi (DB fayli `squidadmin` foydalanuvchisiga yopiq). Saqlangan sirni API orqali o'chirib bo'lmaydi — faqat kanalni o'chirib qo'yish yoki yangisini yozish mumkin
- `logfile_rotate` noldan katta bo'lsa va tizim logrotate'i ham aylantirsa, loglar ikki marta siljishi mumkin (sozlama tavsifida yozilgan)
- Ogohlantirish holati (`squid_down` ketma-ket sanagichi, so'nggi daqiqalar hisoblagichi) xotirada: panel qayta ishga tushsa nolga tushadi
- `refresh_pattern` hali ham qoldirilgan (8-bosqich bilan)

### Audit (1–5 bosqichlardan keyin)  ✅ (topilgan nuqsonlar tuzatildi)
**Tuzatildi** (har biri testda, ko'pi mutatsion tekshiruvda ham tasdiqlangan):
- Jonli log oqimi log aylantirilgandan keyin jim qolardi, yarim yozilgan qatorni ikkiga bo'lardi, xatoni yutardi, qatori cheksiz o'sishi mumkin edi → qayta yozildi (aylantirish/qisqartirishga chidamli), yopilish sababi mijozga yetkaziladi, ko'rish oynalari soni 20 ta bilan cheklangan, ping/pong va o'qish chegarasi qo'shildi; sahifa uzilganda o'zi qayta ulanadi
- HTTP serverda timeout va so'rov hajmi chegarasi yo'q edi (login ochiq endpoint) → `http.Server` timeout'lari, 4 MB tana chegarasi, SIGTERM'da toza to'xtash, `BIND_ADDR`
- Audit logga login'da begona yozgan uzun username/maydonlar cheklovsiz tushardi → maydonlar UTF-8 xavfsiz qisqartiriladi
- Login limiter xotirasi bir marta kelib ketgan IP'lar hisobiga o'sardi → davriy tozalash
- Baza fayli `0644` (ichida parol xeshlari, sessiya tokenlari, ogohlantirish sirlari) → papka `0700`, fayl va WAL/SHM `0600`, eski bazalar ham qayta ochilganda qattiqlashtiriladi
- API javoblariga `no-store`, `nosniff`, `X-Frame-Options: DENY`, `Referrer-Policy` sarlavhalari
- `quic-go` zaifligi (GO-2026-5676) → 0.59.1; `govulncheck`: kodga ta'sir qiluvchi zaiflik 0; `npm audit`: 0 (oxirgi urinishda tarmoq xatosi)
- Repoda qolgan eski `setup-service.sh`, `squidadmin-backend.service` (`User=root`, `/home/dior`), `squidadmin-sudoers` o'chirildi
- Frontend: noma'lum manzil bo'sh sahifa berardi → bosh sahifaga yo'naltiradi; render xatosi butun ilovani oq ekranga aylantirardi → `ErrorBoundary`; `window.prompt` (parol ochiq ko'rinardi) → modal; mobil/planshetda yon menyu drawer bo'ldi; `lang="uz"`

**Ataylab qoldirildi (tavsiya):**
- Panel HTTP'da gaplashadi: production'da TLS reverse proxy (nginx/Caddy) + `BIND_ADDR=127.0.0.1` + `COOKIE_SECURE=true` (8-bosqich)
- `admin` roli xom config orqali Squid foydalanuvchisi nomidan yordamchi dasturlarni ishga tushira oladi (`external_acl_type`, `auth_param ... program`) — bu dizaydan: admin = tizim darajasidagi ishonch, alohida admin berishda shuni bilish kerak
- Config tarixi ~350 KB × 200 versiya ≈ 70 MB (siqilmagan); siqish yoki farq saqlash mumkin
- Sirlar (Telegram token, SMTP paroli) bazada shifrlanmagan; TOTP 2FA yo'q
- Frontend testlari yo'q; 17 ta `eslint-disable` (asosan `exhaustive-deps`); handler'lar alohida unit test'siz (API testlari va E2E orqali qoplangan)
- Ildiz `README.md`, CI, OpenAPI hujjati yo'q (8-bosqich)
- 7d/30d grafik ustunlari UTC chegarasida; manfiy UTC siljishli brauzerda sana yorlig'i bir kunga surilib ko'rinishi mumkin

### 6-bosqich — Foydalanuvchilar va resurs cheklovlari  ✅ (LDAP/AD tashqari — bajarildi)
**Nima qilindi**
- Hisobni **o'chirmasdan to'xtatish**, **amal qilish muddati** (sana kunning oxirigacha amal qiladi), **kunlik hajm kvotasi**, izoh, **foydalanuvchi guruhlari** (`Proxy foydalanuvchilari` sahifasi). Parollar passwd faylida qoladi; qolgan hamma narsa DB'da, `squid.conf`ga esa bitta boshqariladigan blok yoziladi (`user-policy`)
- Bloklash qoidasi faqat login yuborgan so'rovlarda ishlaydi (`req_header Proxy-Authorization`), shuning uchun login yubormagan so'rovlar bu qoida sababli parol so'ramaydi; bloklangan hisob localhost'dan ham, LAN'dan ham rad etiladi
- Muddat va kvota **taymer** (30 soniya, `POLICY_CHECK_SECONDS`) bilan tekshiriladi; Squid faqat bloklanganlar ro'yxati o'zgarganda qayta yuklanadi (yangi hisob shu uchun ham ortiqcha reload qilmaydi). Kvota hajmi `access.log` statistikasidan (soatlik agregat) olinadi
- **Cheklovlar** sahifasi: tezlik (`delay_pools`: har foydalanuvchiga / har qurilmaga / hamma uchun umumiy; KB/s + boshlang'ich "portlash") va bitta fayl hajmi (`reply_body_max_size`); kimga: hamma, foydalanuvchilar, foydalanuvchi guruhlari, IP guruhlari
- **CSV** import/eksport: eksportda parol yo'q va Excel formulalaridan himoya (`=`, `+`, `-`, `@` boshidagi kataklar); importda quruq rejim (`dry_run`), "hammasi yoki hech narsa" (bitta xato qator bo'lsa hech narsa yozilmaydi), 1000 qator / 1 MB chegarasi, Excel BOM/CRLF, mavjud hisobga parol yozilsa almashtiriladi
- Siyosat testeri (`Kirish qoidalari`) yangi blokni tushunadi (`req_header`, manba "Hisob holati")

**Real Squid'da topilgan va tuzatilgan** (`deploy/e2e/phase6.py`, 64 tekshiruv)
- `proxy_auth` qoidasi bilan rad etilgan autentifikatsiyalangan foydalanuvchi **407** oladi (brauzer parolni qayta-qayta so'raydi) → blokka `deny_info 403:ERR_ACCESS_DENIED` qo'shildi, endi oddiy 403
- Ikki panel bloki (vaqt cheklovlari va `user-policy`) ikkalasi ham "birinchi allow'dan oldin"ga yozilsa, har sinxronda o'rin almashib, config har 30 soniyada o'zgarib reload qilardi → aniq tartib: `user-policy → time-restrictions → access rules → stock allows`
- stock `allow localhost` login'ni umuman tekshirmaydi, shuning uchun autentifikatsiya va kvota faqat LAN manzili orqali sinaladi (E2E shunday yozilgan)

**Ataylab qoldirildi / ma'lum cheklovlar**
- **LDAP / Active Directory**: haqiqiy katalog serverisiz autentifikatsiyani tekshirib bo'lmaydi, faqat `squid -k parse` bilan tekshirilgan konfiguratsiya yozish tavakkal — keyinroq (slapd bilan) qilinadi
- Kvota aniqligi: statistika soatlik, shuning uchun 30 daqiqalik siljigan vaqt zonalarida kun boshi hisobi 30 daqiqagacha oldin boshlanishi mumkin; bloklash kvota to'lgandan keyin ~30–60 soniya kechikadi (log o'qish 5 s + taymer 30 s)
- Tezlik cheklovi faqat bitta havza tanlaydi: bir so'rovga birinchi mos qoida ishlaydi (qoidalar tartibi = yaratilish tartibi, tartibni o'zgartirish yo'q)
- Foydalanuvchi guruhlari hozircha faqat cheklovlarda ishlatiladi ("Kirish qoidalari" ACL'lariga bog'lanmagan)
- Bloklangan hisob uchun xato sahifasi umumiy "Access Denied" (o'zgacha matn yo'q)
- Brauzerda `Proxy foydalanuvchilari` va `Cheklovlar` sahifalari ochilib ko'rildi; tugmalar bosilib sinalmadi (API va real Squid orqali sinalgan)

### 7-bosqich — HTTPS va ilg'or imkoniyatlar
- `ssl_bump`: CA generatsiyasi, sertifikatni qurilmalarga tarqatish, istisno ro'yxati
- Transparent proxy yordamchisi (iptables/nftables qoidalari)

### 8-bosqich — Deploy va sifat
- Docker / docker-compose, bitta binar (frontend backend ichida), systemd o'rnatuvchi (qattiq yozilgan yo'llarsiz)
- CI (vet, test, tsc, lint, build), handler va frontend testlari, Playwright E2E
- Ildiz README, OpenAPI hujjati, i18n (uz/ru/en)
- Ko'p serverli boshqaruv (agent arxitekturasi)

## Tavsiya etilgan tartib
1 → 2 → 3 → 4 → 5 → 6 → 8 → 7. (1–6 bajarildi, LDAP qoldi; keyingisi 8, keyin 7.) Har bosqich oxirida haqiqiy Squid'da (WSL) qo'lda va avtomatik tekshiriladi.
