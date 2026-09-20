# Brauzer E2E testlari (Playwright)

Haqiqiy Chromium panelni odam kabi boshqaradi: bosadi, yozadi, natijani ekranda ko'radi
**va** serverda ham tekshiradi (`squid.conf`, haqiqiy proksi orqali `curl`, webhook tinglovchisi).
Har bir test brauzerni ham kuzatadi: skript xatosi, konsol xatosi yoki 5xx javob testni yiqitadi.

## Talablar

- Ishlayotgan panel (odatda `http://localhost:8080`, `deploy/install.sh` o'rnatgan) va Squid.
- **Panel va testlar bir mashinada** bo'lishi kerak: testlar `curl -x 127.0.0.1:3128` bilan haqiqiy
  so'rov yuboradi, webhook tinglovchisini shu mashina LAN manzilida ochadi.
- `01-auth` **admin birinchi ishga tushirish holatida** bo'lishini talab qiladi (bir martalik parol
  hali almashtirilmagan). U parolni almashtiradi va sessiyani `e2e/.auth/admin.json` ga saqlaydi;
  qolgan fayllar shu sessiyadan foydalanadi. Boshqa holatda 01 yiqiladi, 02-14 esa ishlayveradi
  (agar `.auth/admin.json` bor bo'lsa).
- `npm ci && npx playwright install chromium`

## Ishga tushirish

```sh
cd frontend
npx playwright test --headed          # hammasi, ko'rinadigan oynada
SLOWMO=350 npx playwright test e2e/05-users.spec.ts --headed   # sekin, kuzatish uchun
npx playwright test                   # ko'rinmas (headless)
```

| O'zgaruvchi    | Ma'nosi                                                                 |
| -------------- | ----------------------------------------------------------------------- |
| `BASE_URL`     | panel manzili (standart `http://localhost:8080`)                        |
| `E2E_ADMIN_PW` | admin'ning bir martalik boshlang'ich paroli (**01-auth uchun majburiy**) |
| `SLOWMO`       | har bir harakatdan oldin kutish, millisekund                            |

## Fayllar

| Fayl | Nimani tekshiradi |
| ---- | ----------------- |
| `01-auth` | kirish xatolari, parol majburiy almashtirish, chiqish |
| `02-navigation` | 17 sahifa, telefon menyusi |
| `03-blacklist` | domen qo'shish/rad etish/o'chirish, `squid.conf` |
| `04-groups-restrictions` | IP guruhlar, vaqt cheklovlari |
| `05-users` | proksi foydalanuvchilar, guruhlar, CSV, muddat/kvota |
| `06-limits` | tezlik va hajm cheklovlari |
| `07-stats-logs` | statistika, log fayllar, jonli log |
| `08-panel-users-roles` | panel foydalanuvchilar, viewer/operator/admin huquqlari |
| `09-settings-config-history-audit` | Squid sozlamalari, xom config, tarix (tiklash), audit |
| `10-access-rules` | ACL, qoidalar, tartib, shablon, "Nima uchun?" tekshirgichi |
| `11-blocklists` | tashqi ro'yxatlar, ichki manzillar rad etilishi |
| `12-account-settings` | o'z parolini almashtirish, sessiyalarni tugatish |
| `13-alerts` | ogohlantirishlar, haqiqiy uzilish va webhook |
| `14-dashboard` | to'xtatish/ishga tushirish/qayta yuklash, LAN |

## Ehtiyot bo'ling

- Testlar **haqiqiy** Squid'ni to'xtatadi va qayta ishga tushiradi (13, 14) va `squid.conf` ni
  o'zgartiradi. Ular o'zlari yaratgan narsalarni (`e2e_*`) o'chiradi, lekin ishlab turgan
  ish serverida yurgizmang.
- 13-alerts webhookni saqlaydi va oxirida o'chirib qo'yadi; saqlangan manzil qoladi (o'chirilgan holda).
- Hozircha CI'da yurmaydi: brauzer, Squid va systemd bilan to'liq stend kerak.
