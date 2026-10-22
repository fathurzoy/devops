# Tutorial: Deploy, Promosi, dan CI/CD

Dokumen ini menjawab pertanyaan paling praktis: **“kalau ada update kode,
cara menaikkannya bagaimana?”** Di setup kita ada tiga jalur, meniru pola yang
umum di kantor (termasuk pola BRI yang diceritakan): **dev otomatis (CI/CD),
staging & prod promosi manual oleh “DevOps”**.

```text
git push ──> GitHub Actions (CI: vet+build+docker build)
                    │ lolos
                    ▼
repo main ──> VPS menarik sendiri (CD pull-based, tiap 1 menit) ──> DEV naik sendiri
                    │
        promosi manual (checklist + perintah) ──> STAGING ──> PROD
```

## A. Naik Update Secara Manual

### A1. Lewat Portainer (GUI)

Cocok untuk menaikkan **satu container** (mis. hanya order-service) tanpa
terminal:

1. Buka Portainer → **Stacks** → pilih stack env tujuan
   (`example-app-dev` / `example-app-staging` / `example-app`).
2. Kalau yang berubah hanya kode service (image perlu dibangun ulang), cara
   GUI murni tidak membangun image — build tetap lewat CLI sekali
   (`docker compose ... build order-service`), setelah itu di Portainer:
3. **Containers** → centang container-nya → **Recreate** → aktifkan
   *“Pull latest image”* bila image diambil dari registry (di setup kita image
   dibangun lokal, jadi cukup Recreate) → **Recreate**.
4. Tunggu status `running`, lalu verifikasi: buka FE env itu, klik
   **Cek Health Semua Service**, dan cek versi di header FE.
5. Cek log container-nya di Portainer (tombol **Logs**) bila ada yang aneh.

> Catatan penting: menekan **Stop/Start** tidak mengganti image — container
> lama hanya dihidupkan lagi. Untuk update kode selalu perlu **Recreate**
> (atau `docker compose up -d` dari CLI yang otomatis me-recreate container
> yang konfigurasinya berubah).

### A2. Lewat CLI docker compose (cara utama di setup kita)

```bash
cd ~/example-app-staging        # folder env tujuan
docker compose --env-file app.env --env-file secrets.env build order-service
docker compose --env-file app.env --env-file secrets.env up -d order-service
curl -s http://127.0.0.1:18090/api/health/all   # verifikasi (port gateway staging)
```

Untuk menaikkan **semua** service sekaligus, hilangkan nama service di kedua
perintah. `up -d` hanya me-recreate container yang berubah — yang lain tetap
jalan.

## B. CI — GitHub Actions

File: `.github/workflows/ci.yml` di root repo. Isinya berjalan setiap ada
*push* atau *pull request* yang menyentuh `example-app/**`:

1. `actions/checkout` + `setup-go` (versi Go mengikuti `go.mod`).
2. `go vet ./...` — cek statis kode Go.
3. `go build ./...` — semua service harus bisa dikompilasi.
4. `docker build` untuk image gateway & scheduler — memastikan Dockerfile
   sehat.

Cara membaca hasilnya: GitHub → repo → tab **Actions**. Hijau = aman; merah =
klik run-nya, baca step yang gagal (biasanya vet/build), perbaiki, push lagi.
**CI tidak men-deploy apa pun** — dia hanya penjaga gerbang kualitas.

> **Status pengaktifan (22 Okt 2026):** file workflow ini sudah dibuat dan
> lolos validasi YAML, tetapi GitHub menolak push file di folder
> `.github/workflows/` dari token OAuth hasil *device flow* karena token itu
> belum memiliki **scope `workflow`**. Obatnya satu kali saja, pilih salah
> satu: (1) jalankan `gh auth refresh -h github.com -s workflow` lalu
> setujui kode device yang muncul — sesudah itu push file workflow seperti
> biasa; atau (2) unggah file `.github/workflows/ci.yml` lewat antarmuka web
> GitHub (tombol *Add file → Upload files* di repo). Begitu file-nya berada di
> repo, workflow langsung aktif pada push berikutnya.

## C. CD — Auto-Deploy Pull-Based Khusus DEV

Karena VPS tidak membuka port khusus untuk Jenkins/GitHub menembak masuk,
CD kita memakai model **pull** (mirip GitOps/ArgoCD, tapi versi ringan):

- Di VPS ada clone repo: `~/deploy/devops-clone`.
- Script `~/deploy/deploy-dev.sh` dijalankan **cron tiap 1 menit**
  (dengan `flock` supaya tidak tumpang tindih):

  ```cron
  * * * * * /usr/bin/flock -n /tmp/deploy-dev.lock /home/ubuntu/deploy/deploy-dev.sh
  ```

- Script membandingkan commit `origin/main` dengan penanda
  `~/deploy/deployed-dev.commit`. Kalau ada commit baru **dan** menyentuh
  `example-app/**`, script menyalin source ke `~/example-app-dev`
  (**`secrets.env` tidak pernah disentuh**), memperbarui `app.env` dari
  `env/dev.env` di repo, lalu `docker compose up -d --build` untuk env dev.
- Semua kejadian dicatat di `~/deploy/deploy.log`:

  ```bash
  tail -f ~/deploy/deploy.log
  ```

- Mematikan CD sementara: komentari baris cron (`crontab -e`). Menyalakan
  lagi: hilangkan komentarnya. Tidak ada komponen lain yang perlu disentuh.

Alur pakainya sehari-hari: **ubah kode → commit → push → tunggu ≤ ±1 menit →
DEV sudah versi baru.** Coba buktikan: naikkan `APP_VERSION` di
`example-app/env/dev.env` lewat commit, lalu lihat header FE dev berubah
tanpa kamu menyentuh server sama sekali.

## D. Promosi dev → staging → prod (Manual, ala Kantor)

Di kantor, staging & prod biasanya **tidak** punya auto-deploy: developer
*minta naik* ke tim DevOps, DevOps yang mengeksekusi dengan checklist. Di sini
kamu berperan sebagai dua-duanya, tapi prosedurnya dibuat sama:

### Checklist sebelum naik

- [ ] Versi sudah terbukti jalan di env asal (dev untuk ke staging; staging
      untuk ke prod): health OK, case utama di FE lolos.
- [ ] Perubahan database (kalau ada) sudah di-backup
      (lihat `tutorial-backup-restore-copy.md`).
- [ ] `APP_VERSION` di `env/<staging|prod>.env` di repo sudah dinaikkan &
      ter-commit — versi adalah identitas rilis.
- [ ] Jam naik dipilih yang sepi (untuk prod), dan ada rencana rollback.

### Perintah promosi ke staging (contoh)

```bash
# 1. Samakan source & konfigurasi staging dengan yang sudah teruji di dev
rsync -a --exclude 'secrets.env' --exclude 'app.env' ~/example-app-dev/ ~/example-app-staging/
cp ~/example-app-staging/env/staging.env ~/example-app-staging/app.env   # APP_VERSION samakan via repo

# 2. Build + naikkan
cd ~/example-app-staging
docker compose --env-file app.env --env-file secrets.env up -d --build

# 3. Verifikasi
curl -s http://127.0.0.1:18090/api/health/all
```

### Perintah promosi ke prod

Sama persis, ganti folder ke `~/example-app` dan gateway `:18080`, **plus**
satu kehati-hatian ekstra: naikkan per service, verifikasi health tiap
service sebelum lanjut ke service berikutnya:

```bash
cd ~/example-app
for svc in user-service order-service notification-service scheduler-service gateway frontend-fe1 frontend-fe2; do
  docker compose --env-file app.env --env-file secrets.env up -d --build $svc
  curl -s http://127.0.0.1:18080/health >/dev/null && echo "$svc OK"
done
```

### Rollback (kembali ke versi sebelumnya)

1. Kembalikan `APP_VERSION` & source ke commit/tag sebelumnya
   (`git checkout <commit-bagus>` di clone, atau turunkan versi di env file).
2. Jalankan `up -d --build` yang sama. Selesai — container kembali ke versi
   sebelumnya.
3. Untuk order-service saat demo blue-green, rollback bahkan cukup
   mengarahkan upstream gateway kembali ke blue
   (lihat `tutorial-ha-dan-scheduler.md`).

## E. Kalau Memakai Jenkins (Perbandingan)

Tahapannya **persis sama**, beda alatnya saja:

| Tahap | Setup kita | Jenkins |
|---|---|---|
| Trigger | push ke GitHub + cron pull di VPS | webhook GitHub → Jenkins |
| CI | GitHub Actions (vet/build/docker) | Jenkinsfile stage `build & test` |
| CD dev | script pull-based di VPS | Jenkins agent SSH ke VPS |
| Promosi staging/prod | manual + checklist | Jenkins job dengan tombol **Approve** (input step) |
| Catatan rilis | git log + deploy.log | build history Jenkins |

Jadi ilmu yang kamu pelajari di sini (stage, artefak versi, promosi,
rollback) pindah begitu saja ke Jenkins kapan pun VPS-mu di-upgrade.
