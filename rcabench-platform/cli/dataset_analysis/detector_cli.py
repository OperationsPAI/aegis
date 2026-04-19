#!/usr/bin/env -S uv run -s
import json
from pathlib import Path

import polars as pl
from dotenv import load_dotenv

from rcabench_platform.v3.analysis.detector_visualization import batch_visualization
from rcabench_platform.v3.cli.main import app, logger
from rcabench_platform.v3.sdk.datasets.rcabench import valid
from rcabench_platform.v3.sdk.pedestals.train_ticket import TrainTicketPedestal

load_dotenv()


@app.command()
def manual_vis_detector(datapacks: list[Path], skip_existing: bool = False) -> None:
    batch_visualization(datapacks, skip_existing)


@app.command()
def auto_vis_detector(skip_existing: bool = True) -> None:
    datapack_path = Path("data") / "rcabench_dataset"
    if not datapack_path.exists():
        logger.error(f"Datapack directory not found: {datapack_path}")
        return

    valid_datapacks = []
    for p in datapack_path.iterdir():
        if p.is_dir() and valid(datapack_path / p.name):
            valid_datapacks.append(p)

    batch_visualization(valid_datapacks, skip_existing)


# Mapping from URL service names to ground truth service names
URL_TO_GT_SERVICE_MAP: dict[str, list[str]] = {
    "assuranceservice": ["ts-assurance-service"],
    "cancelservice": ["ts-cancel-service"],
    "consignservice": ["ts-consign-service"],
    "contactservice": ["ts-contacts-service"],
    "executeservice": ["ts-execute-service"],
    "foodservice": ["ts-food-service"],
    "inside_pay_service": ["ts-inside-payment-service"],
    "orderOtherService": ["ts-order-other-service"],
    "orderservice": ["ts-order-service"],
    "preserveservice": ["ts-preserve-service"],
    "rebookservice": ["ts-rebook-service"],
    "routeservice": ["ts-route-service"],
    "trainservice": ["ts-train-service"],
    "travel2service": ["ts-travel2-service"],
    "travelplanservice": ["ts-travel-plan-service"],
    "travelservice": ["ts-travel-service"],
    "users": ["ts-user-service"],
    "userservice": ["ts-user-service"],
    "verifycode": ["ts-verification-code-service"],
}


def _extract_url_service(span_name: str) -> str | None:
    """Extract service name from SpanName URL pattern like /api/v1/xxxservice/..."""
    import re

    match = re.search(r"/api/v1/([^/]+)/", span_name)
    return match.group(1) if match else None


def _url_service_matches_gt(url_service: str, gt_services: list[str]) -> bool:
    """Check if a URL service name matches any of the ground truth services."""
    # Get possible GT service names for this URL service
    possible_gt_names = URL_TO_GT_SERVICE_MAP.get(url_service, [])
    # Check if any of the possible GT names is in the ground truth list
    return any(gt_name in gt_services for gt_name in possible_gt_names)


@app.command()
def get_datapacks():
    import json
    from collections import Counter

    import pandas as pd

    dataset_path = Path("data") / "rcabench_dataset"
    if not dataset_path.exists():
        logger.error(f"Dataset directory not found: {dataset_path}")
        return

    # Read each datapack's conclusion.csv and filter those with non-empty issues
    # that do NOT match the ground truth services from injection.json
    valid_datapacks = []
    fault_type_counter: Counter[int] = Counter()
    gt_service_counter: Counter[str] = Counter()

    for p in dataset_path.iterdir():
        if not p.is_dir():
            continue
        conclusion_path = p / "conclusion.csv"
        injection_path = p / "injection.json"
        if not conclusion_path.exists() or not injection_path.exists():
            continue

        df = pd.read_csv(conclusion_path)
        if "Issues" not in df.columns:
            continue

        # Get rows with non-empty Issues
        issues_mask = df["Issues"].dropna().astype(str).str.strip() != "{}"
        if not issues_mask.any():
            continue

        # Load ground truth services from injection.json
        with open(injection_path) as f:
            injection_data = json.load(f)
        gt = injection_data.get("ground_truth")
        if not gt:
            continue
        gt_services = gt.get("service", [])
        if not gt_services:
            continue

        # Check if any SpanName with non-empty Issues does NOT match any ground truth service
        df_with_issues = df[issues_mask]
        has_non_gt_issue = False
        for span_name in df_with_issues["SpanName"]:
            url_service = _extract_url_service(str(span_name))
            if url_service is None:
                # Cannot extract service from URL, treat as non-matching
                has_non_gt_issue = True
                break
            if not _url_service_matches_gt(url_service, gt_services):
                has_non_gt_issue = True
                break

        if has_non_gt_issue:
            valid_datapacks.append(p.name)
            # Collect statistics
            fault_type = injection_data.get("fault_type")
            if fault_type is not None:
                fault_type_counter[fault_type] += 1
            for svc in gt_services:
                gt_service_counter[svc] += 1

    # Save to temp file
    output_path = Path("temp") / "valid_datapacks.txt"
    output_path.parent.mkdir(parents=True, exist_ok=True)
    with open(output_path, "w") as f:
        for dp in valid_datapacks:
            f.write(f"{Path('data') / 'rcabench_dataset' / dp}\n")
    logger.info(f"Saved {len(valid_datapacks)} datapacks to {output_path}")

    # Print statistics
    print("\n" + "=" * 60)
    print(f"Total valid datapacks: {len(valid_datapacks)}")
    print("=" * 60)

    print("\n📊 Fault Type Distribution:")
    print("-" * 40)
    for fault_type, count in sorted(fault_type_counter.items(), key=lambda x: -x[1]):
        pct = count / len(valid_datapacks) * 100
        print(f"  Type {str(fault_type):>3s}: {count:4d} ({pct:5.1f}%)")

    print("\n🔧 Ground Truth Service Distribution:")
    print("-" * 40)
    for svc, count in sorted(gt_service_counter.items(), key=lambda x: -x[1]):
        pct = count / len(valid_datapacks) * 100
        print(f"  {svc:40s}: {count:4d} ({pct:5.1f}%)")


@app.command()
def extract_span_relations(
    dataset_name: str = "rcabench_dataset",
    trace_file_name: str = "normal_traces.parquet",
    output_file: str = "temp/span_relations.json",
    normalize_paths: bool = True,
) -> None:
    """
    Extract all span names and their call relationships from traces.

    Args:
        dataset_name: Name of the dataset folder (default: "rcabench_dataset")
        trace_file_name: Name of the trace parquet file (default: "normal_traces.parquet")
        output_file: Output JSON file path (default: "temp/span_relations.json")
        normalize_paths: Whether to normalize span names using pedestal (default: True)
    """
    dataset_path = Path("data") / dataset_name
    if not dataset_path.exists():
        logger.error(f"Dataset directory not found: {dataset_path}")
        return

    # Initialize pedestal for normalization
    pedestal = TrainTicketPedestal() if normalize_paths else None

    # Collect all span names and relationships
    all_span_names: set[str] = set()
    span_to_services: dict[str, set[str]] = {}  # span_name -> set of services
    call_relationships: set[tuple[str, str]] = set()  # (parent_span, child_span)
    normalized_map: dict[str, str] = {}

    # Find all batch directories (starting with "batch")
    batch_dirs = sorted([d for d in dataset_path.iterdir() if d.is_dir() and d.name.startswith("batch")])
    logger.info(f"Found {len(batch_dirs)} batch directories in {dataset_path}")

    processed_count = 0
    for batch_dir in batch_dirs:
        # Try both trace.parquet and specified trace_file_name
        parquet_paths = [
            batch_dir / trace_file_name,
            batch_dir / "trace.parquet",
        ]

        parquet_path = None
        for p in parquet_paths:
            if p.exists():
                parquet_path = p
                break

        if parquet_path is None:
            continue

        try:
            # Read trace data
            df = pl.read_parquet(parquet_path)

            # Extract span names and services
            if "SpanName" in df.columns:
                span_name_col = "SpanName"
                service_name_col = "ServiceName"
            elif "span_name" in df.columns:
                span_name_col = "span_name"
                service_name_col = "service_name"
            else:
                logger.warning(f"No span name column found in {parquet_path}")
                continue

            if service_name_col not in df.columns:
                logger.warning(f"No service name column found in {parquet_path}")
                continue

            # Collect span names with their services
            for row in df.select([span_name_col, service_name_col]).unique().iter_rows(named=True):
                span_name = row[span_name_col]
                service_name = row[service_name_col]
                if span_name and service_name:
                    all_span_names.add(span_name)
                    if span_name not in span_to_services:
                        span_to_services[span_name] = set()
                    span_to_services[span_name].add(service_name)

            # Extract parent-child relationships
            parent_col = "ParentSpanId" if "ParentSpanId" in df.columns else "parent_span_id"
            span_id_col = "SpanId" if "SpanId" in df.columns else "span_id"

            if parent_col not in df.columns or span_id_col not in df.columns:
                logger.warning(f"Missing span ID columns in {parquet_path}")
                continue

            # Build span_id to span_name mapping
            span_id_to_name = {}
            for row in df.select([span_id_col, span_name_col]).iter_rows(named=True):
                span_id = row[span_id_col]
                span_name = row[span_name_col]
                if span_id and span_name:
                    span_id_to_name[span_id] = span_name

            # Extract relationships
            for row in df.select([parent_col, span_id_col]).iter_rows(named=True):
                parent_id = row[parent_col]
                child_id = row[span_id_col]

                if parent_id and child_id and parent_id in span_id_to_name and child_id in span_id_to_name:
                    parent_name = span_id_to_name[parent_id]
                    child_name = span_id_to_name[child_id]
                    call_relationships.add((parent_name, child_name))

            processed_count += 1

        except Exception as e:
            logger.warning(f"Error processing {parquet_path}: {e}")
            continue

    logger.info(f"Processed {processed_count} trace files")
    logger.info(f"Total distinct span names: {len(all_span_names)}")
    logger.info(f"Total call relationships: {len(call_relationships)}")

    # Normalize span names if requested
    if normalize_paths and pedestal:
        for name in all_span_names:
            normalized = pedestal.normalize_path(name)
            normalized_map[name] = normalized

        # Normalize relationships
        normalized_relationships = set()
        for parent, child in call_relationships:
            norm_parent = normalized_map.get(parent, parent)
            norm_child = normalized_map.get(child, child)
            normalized_relationships.add((norm_parent, norm_child))

        logger.info(
            f"Normalized: {len([1 for orig, norm in normalized_map.items() if orig != norm])} / {len(all_span_names)} span names"  # noqa: E501
        )
        logger.info(f"Normalized relationships: {len(normalized_relationships)}")

        # Use normalized data and update span_to_services mapping
        normalized_span_to_services: dict[str, set[str]] = {}
        for orig_span, services in span_to_services.items():
            norm_span = normalized_map.get(orig_span, orig_span)
            if norm_span not in normalized_span_to_services:
                normalized_span_to_services[norm_span] = set()
            normalized_span_to_services[norm_span].update(services)

        span_to_services = normalized_span_to_services
        unique_span_names = sorted(set(normalized_map.values()))
        relationships = list(normalized_relationships)
    else:
        unique_span_names = sorted(all_span_names)
        relationships = list(call_relationships)
        normalized_map = {name: name for name in all_span_names}

    # Remove pure HTTP method nodes and connect their parents to children
    http_methods = {
        "GET",
        "POST",
        "PUT",
        "DELETE",
        "PATCH",
        "HEAD",
        "OPTIONS",
        "TRACE",
        "CONNECT",
    }

    # Build parent->children and child->parents maps
    from collections import defaultdict

    parent_to_children: dict[str, set[str]] = defaultdict(set)
    child_to_parents: dict[str, set[str]] = defaultdict(set)

    for parent, child in relationships:
        parent_to_children[parent].add(child)
        child_to_parents[child].add(parent)

    # Find HTTP method nodes and their connections
    method_nodes = {name for name in unique_span_names if name in http_methods}

    if method_nodes:
        logger.info(f"Found {len(method_nodes)} HTTP method nodes to remove: {method_nodes}")

        # Build new relationships, skipping HTTP method nodes
        new_relationships = set()

        for parent, child in relationships:
            if parent in http_methods and child in http_methods:
                # Both are HTTP methods, skip
                continue
            elif parent in http_methods:
                # Parent is HTTP method, connect its parents to this child
                for grandparent in child_to_parents[parent]:
                    if grandparent not in http_methods:
                        new_relationships.add((grandparent, child))
            elif child in http_methods:
                # Child is HTTP method, connect this parent to its children
                for grandchild in parent_to_children[child]:
                    if grandchild not in http_methods:
                        new_relationships.add((parent, grandchild))
            else:
                # Neither is HTTP method, keep the relationship
                new_relationships.add((parent, child))

        relationships = sorted(list(new_relationships))
        # Remove HTTP method nodes from span names and services mapping
        unique_span_names = sorted([name for name in unique_span_names if name not in http_methods])
        span_to_services = {name: services for name, services in span_to_services.items() if name not in http_methods}

        logger.info(
            f"After removing HTTP methods: {len(unique_span_names)} span names, {len(relationships)} relationships"
        )

    relationships = sorted(relationships)

    # Build service to span names mapping
    service_to_spans: dict[str, list[str]] = {}
    for span_name, services in span_to_services.items():
        for service in services:
            if service not in service_to_spans:
                service_to_spans[service] = []
            service_to_spans[service].append(span_name)

    # Sort span names for each service
    for service in service_to_spans:
        service_to_spans[service] = sorted(service_to_spans[service])

    # Sort services by name
    service_to_spans = dict(sorted(service_to_spans.items()))

    # Build output structure
    output_data = {
        "metadata": {
            "dataset": dataset_name,
            "trace_file": trace_file_name,
            "normalized": normalize_paths,
            "total_span_names": len(unique_span_names),
            "total_services": len(service_to_spans),
            "total_relationships": len(relationships),
            "processed_files": processed_count,
        },
        "services": service_to_spans,
        "call_relationships": [{"parent": parent, "child": child} for parent, child in relationships],
    }

    if normalize_paths:
        # Add normalization mapping for reference
        changed_mappings = {orig: norm for orig, norm in normalized_map.items() if orig != norm}
        output_data["normalization_map"] = changed_mappings

    # Save to JSON
    output_path = Path(output_file)
    output_path.parent.mkdir(parents=True, exist_ok=True)

    with open(output_path, "w", encoding="utf-8") as f:
        json.dump(output_data, f, indent=2, ensure_ascii=False)

    logger.info(f"Saved span relations to {output_path}")
    print(f"✅ Extracted {len(unique_span_names)} unique span names")
    print(f"✅ Extracted {len(service_to_spans)} services")
    print(f"✅ Extracted {len(relationships)} call relationships")
    print(f"✅ Saved to: {output_path}")


if __name__ == "__main__":
    app()
