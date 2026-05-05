terraform {
  backend "s3" {
    bucket       = "terraform-state-nickawilliams"
    key          = "525999333867/common/infrastructure/terraform.tfstate"
    region       = "us-west-1"
    use_lockfile = true
  }
}
