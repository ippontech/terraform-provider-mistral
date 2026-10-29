resource "mistral_skill" "extract_invoices" {
  name        = "extract-invoices"
  description = "Use it to read the totals of an invoice in PDF."
  body        = <<-EOT
    Run scripts/extract.py on the invoice, then answer with its totals.
    Convert them to euros with the rates of data/rates.csv.gz.
  EOT

  assets = {
    # A text file: content, such as file("skill/extract.py").
    "scripts/extract.py" = {
      content    = <<-EOT
        import sys
        print(open(sys.argv[1], "rb").read()[:100])
      EOT
      executable = true
    }
    # A binary file: content_base64, such as filebase64("skill/logo.png").
    "data/rates.csv.gz" = {
      content_base64 = base64gzip("currency,rate\nUSD,1.08\n")
    }
  }

  notes = "Convert the totals to euros"
}
