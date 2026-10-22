# Tutorial: Backup, Restore, dan Menyalin Data Antar Env

Backup itu bukan file-nya — backup adalah **kemampuan untuk kembali** ke
keadaan sebelumnya. Karena itu di tutorial ini, setiap cara backup selalu
dipasan dengan cara restore-nya, dan di akhir kita benar-benar menguji restore
ke database sementara.

## Yang Perlu Di-backup dari Example App

| Komponen | Isi | Cara backup |
|---|---|---|
| PostgreSQL (per env) | user, order | `pg_dump` → file `.sql.gz` |
| `secrets.env` (per env) | password & token | salin file, simpan di tempat aman (password manager / media terenkripsi) — **tidak** ke Git |
| `app.env` + `docker-compose.yml` + source | konfigurasi & kode | sudah otomatis aman: semuanya ada di Git |
| Volume RabbitMQ | antrean pesan | umumnya tidak perlu: pesan di setup kita sifatnya event sesaat; yang penting datanya sudah tersimpan di PostgreSQL oleh service |

## Backup PostgreSQL dengan Script

Sudah disediakan `example-app/scripts/backup-db.sh`:

```bash
bash ~/example-app/scripts/backup-db.sh dev
bash ~/example-app/scripts/backup-db.sh staging
bash ~/example-app/scripts/backup-db.sh prod
```

Hasilnya masuk ke `~/backups/example/<env>/backup-<tanggal-jam>.sql.gz`,
sudah termasuk verifikasi `gzip -t` dan ukuran file dicetak. Di balik layar
script ini menjalankan:

```bash
docker exec <container-postgres-env> pg_dump -U example -d exampledb --clean --if-exists | gzip > file.sql.gz
```

Opsi `--clean --if-exists` membuat file backup bisa di-restore di atas
database yang sudah berisi data (tabel lama di-drop dulu, tidak dobel).

Cara manualnya (kalau mau paham isinya):

```bash
docker exec example-postgres pg_dump -U example -d exampledb | head -50   # lihat format dump
```

Nama container per env: `example-postgres` (prod), `example-staging-postgres`,
`example-dev-postgres`.

## Jadwal Backup Otomatis (cron)

Contoh: backup prod tiap hari jam 03:00, simpan 7 hari terakhir:

```cron
0 3 * * * /home/ubuntu/example-app/scripts/backup-db.sh prod >> /home/ubuntu/backups/backup.log 2>&1
30 3 * * * find /home/ubuntu/backups/example/prod -name 'backup-*.sql.gz' -mtime +7 -delete
```

## Restore

```bash
bash ~/example-app/scripts/restore-db.sh staging ~/backups/example/staging/backup-20261022-030000.sql.gz
```

Script akan meminta kamu mengetik `restore-staging` sebagai konfirmasi —
karena restore **menimpa** database tujuan. Sesudah restore, selalu
verifikasi: buka FE env itu, cek daftar user & order, dan jalankan health.

### Uji restore (yang benar-benar kami lakukan di dev)

Restore tidak harus ke database aslinya. Pola uji yang aman:

```bash
# 1. Buat database sementara di PostgreSQL dev
docker exec example-dev-postgres psql -U example -c 'CREATE DATABASE scratch_restore;'
# 2. Restore file backup ke database sementara itu
gunzip -c ~/backups/example/dev/backup-XXXX.sql.gz | docker exec -i example-dev-postgres psql -U example -d scratch_restore
# 3. Periksa tabel & isinya
docker exec example-dev-postgres psql -U example -d scratch_restore -c '\dt'
# 4. Hapus database sementara
docker exec example-dev-postgres psql -U example -c 'DROP DATABASE scratch_restore;'
```

Kalau langkah 2–3 berhasil, backup-mu **terbukti** bisa dipakai. Lakukan uji
ini berkala, bukan cuma pas panik.

## Menyalin Data Antar Env (mis. dev → staging)

Kadang staging ingin diisi data yang mirip dev untuk pengujian. Caranya
sebenarnya cuma backup + restore lintas env:

```bash
bash ~/example-app/scripts/backup-db.sh dev
bash ~/example-app/scripts/restore-db.sh staging ~/backups/example/dev/backup-<terbaru>.sql.gz
```

Peringatan penting:

- **Arah sebaliknya berbahaya**: menyalin data **prod → dev/staging**
  menyebarkan data asli ke lingkungan yang lebih longgar. Kalau terpaksa,
  anonimkan dulu (ganti nama/email dengan data samaran).
- Restore lintas env menimpa **seluruh** database tujuan — pastikan env
  tujuan memang boleh ditimpa.
- Password database tiap env berbeda, tapi itu tidak menghalangi salin data:
  dump berisi SQL polos (DDL + INSERT), bukan kredensial.

## Backup Konfigurasi & Secrets

- `app.env` per env: aman, ada di Git (`example-app/env/*.env`).
- `secrets.env` per env: salin ke tempat aman di luar server, contohnya
  password manager. Tanpa file ini, database hasil restore tidak akan bisa
  dihubungkan oleh service (password di dalamnya tidak cocok).
- Ingat selalu: `.env`/`secrets.env` **tidak masuk Git** — `.gitignore` repo
  sudah mengecualikannya; kalau suatu hari kamu melihat file secrets
  ter-commit, anggap password itu bocor dan **ganti segera**.
