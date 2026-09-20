// Labels and help texts for the squid settings form. The backend schema is
// language-neutral (keys, kinds, validation); everything the admin reads lives
// here, so translating the panel later means editing this one file.

export const groupMeta: Record<string, { label: string; description: string }> = {
  network: {
    label: "Tarmoq",
    description: "Squid qaysi portlarda ishlaydi va tarmoqqa qanday ko'rinadi",
  },
  cache: {
    label: "Kesh",
    description: "Ko'p so'raladigan sahifalar va fayllar qayerda saqlanadi",
  },
  timeouts: {
    label: "Timeout'lar",
    description: "Ulanishlar qancha kutiladi",
  },
  logs: {
    label: "Jurnallar",
    description: "Log fayllarni aylantirish",
  },
  upstream: {
    label: "Yuqori proksi",
    description: "Trafikni boshqa proksi orqali o'tkazish",
  },
};

export const settingMeta: Record<string, { label: string; help: string }> = {
  http_port: {
    label: "Proksi portlari",
    help: "Squid mijozlar ulanishini shu portlarda kutadi. Portni olib tashlasangiz, unga ulangan qurilmalar uziladi. Masalan: 3128, 0.0.0.0:3129 yoki 3130 intercept (shaffof rejim).",
  },
  visible_hostname: {
    label: "Ko'rinadigan host nomi",
    help: "Xato sahifalarida va Via sarlavhasida ko'rinadi. Bo'sh qolsa, Squid tizim nomini o'zi aniqlaydi.",
  },
  dns_nameservers: {
    label: "DNS serverlar",
    help: "Bo'sh bo'lsa, tizim sozlamalari (/etc/resolv.conf) ishlatiladi. Noto'g'ri manzil kiritilsa saytlar ochilmay qoladi, Squid buni oldindan bilolmaydi.",
  },
  forwarded_for: {
    label: "Mijoz IP manzilini uzatish",
    help: "X-Forwarded-For sarlavhasi. on: mijoz IP'si uzatiladi; off: \"unknown\" yoziladi; delete: sarlavha butunlay olib tashlanadi.",
  },
  via: {
    label: "Via sarlavhasi",
    help: "off qilinsa, Squid javoblarga \"Via\" sarlavhasini qo'shmaydi.",
  },
  logfile_rotate: {
    label: "Saqlanadigan eski loglar soni",
    help: "Loglar aylantirilganda (panel yoki logrotate orqali) nechta eski nusxa saqlanadi: access.log.0, .1 ... 0 bo'lsa Squid fayllarni o'zi qayta nomlamaydi, faqat qayta ochadi — Debian'da nomini tizim logrotate'i o'zgartiradi. Noldan katta qiymat qo'ysangiz, ikkalasi ham aylantirsa loglar ikki marta siljishi mumkin.",
  },
  cache_mem: {
    label: "Kesh uchun operativ xotira",
    help: "Squid keshlangan obyektlarni xotirada saqlaydigan hajm (KB, MB yoki GB). Server xotirasidan oshirmang.",
  },
  maximum_object_size: {
    label: "Eng katta obyekt hajmi",
    help: "Bundan katta fayllar keshlanmaydi.",
  },
  cache_dir: {
    label: "Diskli kesh",
    help: "Format: tur yo'l hajm(MB) L1 L2. Bo'sh: faqat xotirada kesh. O'zgartirish Squid'ni qayta ishga tushirishni talab qiladi; papkani Squid o'zi yaratadi. Yo'l yaratib bo'lmaydigan bo'lsa, o'zgarish avtomatik bekor qilinadi.",
  },
  connect_timeout: {
    label: "Ulanish timeout'i",
    help: "Manzilga ulanish uchun kutish vaqti.",
  },
  read_timeout: {
    label: "O'qish timeout'i",
    help: "Server javob bermay turishi mumkin bo'lgan eng uzun vaqt.",
  },
  request_timeout: {
    label: "So'rov timeout'i",
    help: "Mijoz so'rovni to'liq yuborishi uchun kutish vaqti.",
  },
  shutdown_lifetime: {
    label: "To'xtatishda kutish vaqti",
    help: "To'xtaganda Squid ochiq ulanishlarni shuncha kutadi. Qisqartirilsa, to'xtatish va qayta ishga tushirish tezlashadi.",
  },
  cache_peer: {
    label: "Yuqori proksilar (cache_peer)",
    help: "Format: host parent|sibling http-port icp-port [opsiyalar]. Parolli (login=) ulanish qo'llab-quvvatlanmaydi.",
  },
  never_direct: {
    label: "Faqat yuqori proksi orqali chiqish",
    help: "Yoqilsa, hech bir so'rov to'g'ridan-to'g'ri internetga chiqmaydi. Kamida bitta cheklovchi yuqori proksi kerak.",
  },
};

export const sourceLabels: Record<string, string> = {
  panel: "panel orqali",
  "squid.conf": "squid.conf'dan",
  default: "standart",
};
