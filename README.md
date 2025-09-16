# Azure Spot Monitor

A Kubernetes tool to monitor **Azure Spot VM** availability and automatically update the Kubernetes **Cluster Autoscaler** configmap to prefer spot instances when they are available.

This project includes a **Helm chart** for easy installation into Kubernetes.

---

## Features

- Periodically checks spot VM availability in your Azure subscription using [Spot Placement Score API](https://learn.microsoft.com/en-us/azure/virtual-machine-scale-sets/spot-placement-score?tabs=portal).  
- Updates Kubernetes Cluster Autoscaler configmap using weights for the most available instance types.  
- Lightweight, designed to run as a controller inside your cluster.  
- Ships with a Helm chart for simple deployment.  

---

## How It Works

1. The controller queries [Azure Spot Placement Score API](https://learn.microsoft.com/en-us/rest/api/compute/spot-placement-scores/post?view=rest-compute-2025-02-01-preview&tabs=HTTP) to determine if spot VM SKUs are available.  
2. It calculates whether the Cluster Autoscaler should prefer spot instances.  
3. It updates the Cluster Autoscaler configuration dynamically.  
4. Runs continuously inside the cluster to keep preferences up to date.  

---

## Prerequisites

- A running Kubernetes cluster (v1.22+ recommended).  
- Cluster Autoscaler deployed and configured in your cluster.  
- Azure credentials available in the cluster (via Service Principal or Managed Identity).   
 - You must have access to an Azure subscription where you can grant roles.  
 - A **Managed Identity** (User Assigned or System Assigned) must be created in Azure.  
 - That Managed Identity needs to have the **Compute Recommendations** role assigned so it can call the Spot Placement Score APIs.  
 - The Managed Identity should also have permissions (via Azure RBAC) to read spot VM availability and quotas in the target subscription and region(s).  

 📖 See [Azure Docs: Spot Placement Score](https://learn.microsoft.com/en-us/azure/virtual-machine-scale-sets/spot-placement-score?tabs=portal) for guidance.

---
